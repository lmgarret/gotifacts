---
name: publish-to-gotifacts
description: >-
  Publish a self-contained HTML page to a gotifacts instance and return its
  public URL. Use when the user asks to "publish this", "put this on
  gotifacts", "share this as a web page", or similar, and a self-contained
  HTML artifact is available or can be produced. Requires GOTIFACTS_URL and
  GOTIFACTS_API_KEY in the environment.
---

# Publish to gotifacts

This skill uploads a single self-contained `index.html` to a
[gotifacts](https://github.com/lmgarret/gotifacts) instance via its machine
ingest API, then reports the resulting public URL.

> **Surface note.** This skill needs `GOTIFACTS_URL` and `GOTIFACTS_API_KEY` in
> the environment, so it works in Claude Code, CI, and the API code-execution
> tool — anywhere *you* control the environment. It does **not** work in default
> claude.ai / Claude mobile conversations, which cannot inject those variables.
> For those, enable the gotifacts **MCP connector** (`GOTIFACTS_MCP_ENABLED`) and
> add `https://<base-domain>/mcp` as a custom connector — it authenticates via
> OAuth and exposes an equivalent `publish_site` tool. See the project README.

## Required environment

| Variable | Meaning |
| --- | --- |
| `GOTIFACTS_URL` | Base URL of the gotifacts instance, e.g. `https://example.com` |
| `GOTIFACTS_API_KEY` | A **publish**-scoped API key, ideally group-restricted to `claude` |

You only ever handle the **API key + URL**. Never ask for, store, or transmit
the server's reverse-proxy credentials, SSO secrets, or admin tokens.

## Procedure

### 1. Ask for consent FIRST (required)

Before sending anything to the network, summarize what will be published and
ask the user to confirm. Example:

> I'm about to publish a page titled **"<title>"** to
> `https://<slug>.<group>.<base>` on your gotifacts instance. Proceed?

Do not upload until the user explicitly agrees.

### 2. Produce a self-contained `index.html`

Inline all CSS and JS (and small assets as data URIs) so the page renders
standalone. Save it locally, e.g. `index.html`.

### 3. Pick a URL-safe `slug` and `group`

- `slug`: the leaf name, e.g. `quarterly-report`.
- `group`: defaults to `claude`. May be one or two segments (e.g. `claude` or
  `claude/demos`).
- Every label must match `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`, and the total
  depth (group segments + slug) must be **≤ 3**.

The final URL will be `https://<slug>.<group-reversed>.<base>`. For example
`group=claude`, `slug=report` → `https://report.claude.<base>`.

### 4. Build the `meta` JSON

```json
{
  "group": "claude",
  "slug": "report",
  "title": "Quarterly Report",
  "description": "Optional short description",
  "tags": ["claude", "report"]
}
```

`title` is recommended; `description`, `date`, `tags`, `repo`, `preview`, and
`hidden` are optional.

### 5. POST to the ingest endpoint (single-`index` form)

```sh
curl -fsS \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  -F 'meta=<meta.json;type=application/json' \
  -F 'index=@index.html;type=text/html' \
  "${GOTIFACTS_URL}/ingest/sites"
```

Notes:
- Write the JSON to `meta.json` and reference it with `meta=<meta.json` (the
  leading `<` makes curl read the field value from the file).
- Use `index=@index.html` to upload the HTML file as the `index` part.
- A successful response is JSON: `{"url": "...", "group": "...", "slug": "...", "updated_at": "..."}`.

### 6. Report the result

Tell the user the returned `url`. Re-publishing the same `group`/`slug`
replaces the existing site (idempotent).

## Failure handling

- `401` → the API key is missing or invalid. Ask the user to check
  `GOTIFACTS_API_KEY`.
- `403` → the key is not permitted to publish to that group (it is
  group-restricted). Pick a slug/group within the allowed subtree.
- `400` → invalid path (bad label or too deep) or missing `index.html`. Fix the
  slug/group or the HTML and retry.

## Installation

Copy this skill into your Claude skills directory:

```sh
mkdir -p ~/.claude/skills/publish-to-gotifacts
cp SKILL.md ~/.claude/skills/publish-to-gotifacts/
```

Then set `GOTIFACTS_URL` and `GOTIFACTS_API_KEY` in your environment before use.
