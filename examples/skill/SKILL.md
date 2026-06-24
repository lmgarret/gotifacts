---
name: gotifacts
description: >-
  Publish, unpublish, update, roll back, restore, or purge sites on a gotifacts
  instance. Use when the user asks to "publish this", "put this on gotifacts",
  "share this as a web page", "take that down", "restore that site", "delete it
  permanently", "update the description", or similar.
  The golden path is the gotifacts MCP connector (OAuth, no env vars required).
  Falls back to GOTIFACTS_URL + GOTIFACTS_API_KEY for CI and environments
  without an MCP connector configured.
---

# gotifacts skill

This skill manages sites on a [gotifacts](https://github.com/lmgarret/gotifacts)
instance: publish, unpublish, update metadata, roll back to a prior version,
restore from quarantine, and permanently purge.

## Paths

### Golden path — MCP connector

When a gotifacts MCP connector is active in the session, the tools
`publish_site`, `unpublish_site`, `update_site`, `rollback_site`,
`restore_site`, and `purge_site` are available directly. **Use those tools
instead of curl.** No env vars or API keys are needed; the connector
authenticates via OAuth and its grants already scope what the connection is
allowed to do.

Proceed to the [Operations](#operations) section and substitute the
corresponding MCP tool call for each curl command.

### Fallback — API key + curl

Use this path in Claude Code, CI pipelines, or any environment where the MCP
connector is not configured.

| Variable | Meaning |
| --- | --- |
| `GOTIFACTS_URL` | Base URL of the gotifacts instance, e.g. `https://example.com` |
| `GOTIFACTS_API_KEY` | A scoped API key with the capability required for the operation |

You only ever handle the **API key + URL**. Never ask for, store, or transmit
the server's reverse-proxy credentials, SSO secrets, or admin tokens.

---

## Consent (always required first)

Before any network operation, tell the user what you are about to do and wait
for explicit confirmation. Example:

> I'm about to publish a page titled **"Quarterly Report"** to
> `https://report.claude.example.com`. Proceed?

Do not act until the user agrees.

---

## Operations

### Publish (create or replace)

**MCP:** call `publish_site` with `slug`, `html`, and optional `title`,
`description`, `tags`, `group`.

**API key — curl:**

1. Produce a self-contained `index.html` (inline all CSS/JS; small images as
   data URIs).

2. Pick a URL-safe `slug` and `group`:
   - `slug`: leaf name, e.g. `quarterly-report`.
   - `group`: defaults to `claude`. One or two dot-separated segments.
   - Every label must match `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`; total depth
     (group segments + slug) must be **≤ 3**.
   - Final URL: `https://<slug>.<group-reversed>.<base>`.
     Example: `group=claude`, `slug=report` → `https://report.claude.<base>`.

3. Build `meta.json`:

   ```json
   {
     "group": "claude",
     "slug": "report",
     "title": "Quarterly Report",
     "description": "Optional short description",
     "tags": ["claude", "report"]
   }
   ```

4. POST:

   ```sh
   curl -fsS \
     -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
     -F 'meta=<meta.json;type=application/json' \
     -F 'index=@index.html;type=text/html' \
     "${GOTIFACTS_URL}/ingest/sites"
   ```

   Successful response: `{"url": "...", "group": "...", "slug": "...", "updated_at": "..."}`.

5. Report the returned `url` to the user. Re-publishing the same `group`/`slug`
   is idempotent (replaces the existing site).

---

### Unpublish (soft-delete)

Takes the site offline immediately. Files are kept in a server-side quarantine
for a configurable grace period (default 30 days) before permanent removal.
Re-publishing the same slug during that window restores the site.

**MCP:** call `unpublish_site` with `slug` and optional `group`.

**API key — curl:**

```sh
curl -fsS -X DELETE \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>"
```

A `204 No Content` response confirms the site is offline. Requires the
`unpublish` capability on the key's grant.

---

### Update metadata

Updates title, description, tags, or the hidden flag without replacing the site
content. Use `publish_site` / re-POST if you need to change the actual HTML.

**MCP:** call `update_site` with `slug`, optional `group`, and any of
`title`, `description`, `tags`, `hidden`.

**API key — curl:**

```sh
curl -fsS -X PATCH \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"title": "New Title", "tags": ["updated"]}' \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>"
```

Omitted fields are left unchanged. Requires the `patch` capability.

---

### Roll back

Restores the most recent archived version of the site, swapping current live
content into the version history. Requires versioning to be enabled on the
gotifacts instance (`GOTIFACTS_VERSIONING_ENABLED=true`).

**MCP:** call `rollback_site` with `slug` and optional `group`.

**API key — curl:**

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>/rollback"
```

Requires the `rollback` capability.

---

### Restore (undo unpublish)

Brings a soft-deleted site back online within the server's grace period.
Moves quarantined files back to live and clears the `deleted_at` flag.

**MCP:** call `restore_site` with `slug` and optional `group`.

**API key — curl:**

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>/restore"
```

Returns the restored site object. Requires the `publish` capability (restoring
a site is equivalent to republishing it).

---

### Purge (permanent delete)

Immediately and irreversibly destroys a soft-deleted (quarantined) site and its
files. **This cannot be undone.** The site must already be unpublished — calling
purge on a live site returns `404`.

**MCP:** call `purge_site` with `slug` and optional `group`.

**API key — curl:**

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>/purge"
```

A `204 No Content` response confirms permanent deletion. Requires the `purge`
capability.

---

## Failure handling

| Code | Cause | Fix |
| --- | --- | --- |
| `401` | Key missing or invalid | Check `GOTIFACTS_API_KEY` |
| `403` | Key not permitted for this group/capability | Use a slug/group within the allowed subtree, or request a key with the right capability |
| `400` | Invalid path or missing `index.html` | Fix the slug/group label or the HTML |
| `404` | Site does not exist | Verify the group and slug |

---

## Installation (API key path)

Copy this skill into your Claude skills directory:

```sh
mkdir -p ~/.claude/skills/gotifacts
cp SKILL.md ~/.claude/skills/gotifacts/
```

Then set `GOTIFACTS_URL` and `GOTIFACTS_API_KEY` in your environment before
use. For the MCP path, add `https://<base-domain>/mcp` as a custom connector
in Claude — no further configuration is needed once OAuth consent is granted.
