---
title: Connect Claude via MCP
description: Enable the OAuth-protected MCP connector so claude.ai mobile/web can publish.
sidebar:
  order: 7
---

For the consumer Claude apps (claude.ai mobile/web), the
[skill](/gotifacts/guides/publish-with-claude-skill/) can't inject environment
variables. Instead, expose gotifacts' **MCP connector**: an OAuth 2.1-protected
[Model Context Protocol](https://modelcontextprotocol.io) server at `/mcp` that
exposes the full site lifecycle as tools (`publish_site`, `update_site`,
`unpublish_site`, `list_revisions`, `rollback_site`, `restore_site`,
`purge_site`) and lets an agent browse the content of sites it was granted
read access to (`list_sites`, `list_files`, `read_file`, `search_site`).
Claude's "custom connector" UI authenticates remote MCP servers exclusively via OAuth, so
this is the only path that works on mobile/web.

## 1. Enable it on the server

Set in your environment (see the [configuration reference](/gotifacts/reference/configuration/)):

```ini
GOTIFACTS_MCP_ENABLED=true
# Who may grant connector consent. Keep this tight — it decides who can publish.
GOTIFACTS_MCP_ALLOWED_USERS=you@example.com
```

It requires `GOTIFACTS_TRUSTED_PROXIES` (the consent step is forward-auth'd) and
falls back to `GOTIFACTS_ADMIN_USERS` if no allowlist is set.

## 2. Route it in your proxy

The MCP endpoints split across the two planes:

| Endpoint | Forward-auth | Why |
| --- | --- | --- |
| `/mcp/oauth/authorize` | **ON** | browser consent, authenticated by your SSO |
| `/mcp`, `/mcp/oauth/token`, `/mcp/oauth/register`, `/.well-known/oauth-*` | **OFF** | called server-to-server by Claude (OAuth-guarded) |

The [nginx](/gotifacts/guides/reverse-proxy-nginx/) and
[Caddy](/gotifacts/guides/reverse-proxy-caddy/) guides show the exact patterns.

## 3. Connect from Claude

In Claude → **Settings → Connectors → Add custom connector**, enter
`https://<your-base-domain>/mcp`. Use the **canonical** `GOTIFACTS_BASE_DOMAIN`
here even if you serve [alias domains](/gotifacts/reference/configuration/) — the
OAuth issuer is always the canonical domain. Complete the SSO consent. It is the
same grant editor as the [API keys](/gotifacts/reference/permissions/) screen: a
list of **grant cards**, each with

- a **scope** — a **group** subtree, a **single site**, or **global** (all
  sites). The first grant is prefilled with the group from `GOTIFACTS_MCP_GROUP`
  (default `claude`); the target field suggests your existing groups and sites, and
  you can type a new one.
- the **permissions** to allow on that scope: `publish`, `unpublish`, `rollback`,
  `patch`, `purge` and `read`. The first grant starts with `publish` ticked.

Use **+ Add grant** to give one connector different permissions on different
scopes — for example `publish` on `claude` and `read` on `docs` — and the ✕ on a
card to remove it. Each card needs a target (except global) and at least one
permission, and approving with no grants is rejected.

`read` is a regular permission like the others. It lets the connector read site
content (see [Reading site content](#reading-site-content)), applies to MCP only,
and is **not** implied by any other permission: publishing to `claude` does not let
the connector read `claude` back, so tick `read` on that grant too if you want it.
For a read-only connection, tick only `read`.

A **global** grant covers every site on the instance. The card turns amber and a
warning appears; you must tick **I understand the risk of granting access to all
sites** before **Approve** is enabled, exactly as when creating an API key. Prefer
a narrow group or site where you can.

The connector can never act outside what you granted. Then ask Claude to publish
a page. `publish_site` handles both a **single self-contained page** (the `html`
argument) and a **multi-file site** — an `index.html` plus separate CSS/JS/image
assets — via a `files` array of `{path, content, encoding}` objects (`encoding`
is `utf8` or `base64` for binary assets).

:::note[When to use MCP vs. an archive upload]
Both `html` and `files` travel *inside* the MCP tool call, so their practical
size ceiling is what fits in a single JSON-RPC request body — and `base64`
inflates binary assets by roughly a third. That body is bounded by
`GOTIFACTS_MAX_UPLOAD_BYTES` (default 64 MiB), the same limit as the ingest API,
so the effective site size over MCP is smaller once encoding overhead is
counted. They're ideal for small, generated sites. For a large or asset-heavy
site (a built `dist/` with real images or fonts), publish a `.tar.gz`/`.zip`
**`bundle`** through the [ingest API](/gotifacts/guides/publish-from-ci/)
instead: it streams an already-compressed archive rather than inflating it into
JSON. Both paths then enforce the extraction limits `GOTIFACTS_MAX_EXTRACT_BYTES` (256 MiB)
and `GOTIFACTS_MAX_EXTRACT_ENTRIES` (10 000) — see the
[configuration reference](/gotifacts/reference/configuration/).
:::

For the **API MCP connector** or **Claude Code**, the same server works with a
token obtained through the OAuth flow.

## Reading site content

With a grant that includes the `read` permission (ticked on the consent screen, above),
the connector can browse published sites **without going through forward-auth**.
That matters for sites behind SSO that serve LLM-friendly docs: an agent's own
HTTP fetch has no SSO session, but the MCP endpoint authenticates with its own
OAuth token and gotifacts reads the files straight from disk. Four read-only
tools are added. They read the **live revision** only; archived revisions and
unpublished sites are not readable.

| Tool | What it does |
| --- | --- |
| `list_sites` | Discover readable sites: `group`, `slug`, `url`, title, description, tags, `hidden`, and whether the site serves `llms_txt` / `llms_full_txt`. Filter with `query`, `tag`, `group`; page with `limit` (default 50, max 200) and `offset`. |
| `list_files` | Flat, sorted list of a site's files as `{path, size}`. Restrict with `prefix`; `limit` defaults to 500 (max 2000), and `truncated` says whether more exist. |
| `read_file` | Read one **text** file. With no `path` it returns `llms.txt` if the site has one, else `index.html`; a directory path (or one ending in `/`) resolves to its `index.html`, and a leading `/` is ignored. Binary files are refused. Pages are 64 KiB by default (`limit`, max 256 KiB): when `truncated` is true, pass `next_offset` as `offset` to continue. |
| `search_site` | Case-insensitive plain-text search (no regex) over a site's text files, returning `{path, line, text}` per matching line. Binary files and files over 2 MiB are skipped. `max_results` defaults to 50 (max 200). |

Start with `llms.txt`: call `list_sites`, pick a site marked `llms_txt`, then
`read_file` with just the `slug` and `group`. Follow its links with `read_file`,
or use `search_site` instead of reading a large `llms-full.txt` end to end.

Things to know:

- **Pass the `group` that `list_sites` returned.** For `list_files`, `read_file`
  and `search_site`, an empty `group` defaults to `GOTIFACTS_MCP_GROUP`, exactly
  like the publishing tools. `list_sites` has no such default: empty means all
  groups. Sites with no group (served at `<slug>.<base-domain>`) show up in
  `list_sites` but cannot be addressed by the other three tools.
- **Access is exactly the read grant.** A site the connection may not read, a
  missing site and an unpublished site all return the same *not found or not
  readable by this connection* error, so the tools cannot be used to discover
  which sites exist. Publish-only connections cannot read anything.
- **Hidden sites** are left out of `list_sites` unless you pass
  `include_hidden: true`, but a read grant that covers a hidden site can read it,
  like any other site.
- Output is raw text: HTML is not converted or rendered.
- Content returned by these tools enters the agent's context, so a site can try
  to steer the agent. See [prompt injection](/gotifacts/explanation/security/#reading-site-content-over-mcp-and-prompt-injection).

## Confirming a purge

`purge_site` is the one irreversible tool: it destroys a quarantined site's
files immediately, with no retention window left to recover from. So the server
does not act on the first call. It answers with an *input request*, the client
asks the user to confirm, and the call completes only once the answer comes
back — all inside the same tool call, so the model sees one result, not two.

Confirmation needs a client that can prompt, which it signals by advertising the
elicitation capability. Claude's connectors do. A caller that cannot be prompted
— a CI script driving the [ingest API](/gotifacts/guides/publish-from-ci/) with
an API key, say — purges in a single call as before, which is why a `purge`
grant is worth handing out narrowly (see the
[permissions reference](/gotifacts/reference/permissions/)).

The confirmation is bound to the connection, the exact site and a five-minute
window, so an answer cannot be replayed against a different site or reused
later. Declining leaves the site quarantined and recoverable with
`restore_site`.

## Manage connections

Each consent creates a *connection* you can review and revoke. Admins see them
in the portal's **Connections** view, via `GET /api/mcp/connections`, or
headless:

```sh
gotifacts mcp connections        # list active connections
gotifacts mcp revoke --id <id>   # revoke one — access is lost immediately
```

## How it fits the model

Tokens carry the same **grant model** as [API keys](/gotifacts/reference/permissions/):
the consent screen is the gate that decides *who* may publish, and the chosen
target + capabilities decide *what*. Dynamic Client Registration (RFC 7591) lets
users add the connector by pasting the URL; it only issues a `client_id` and
grants no access on its own. For the deeper rationale see the
[auth model](/gotifacts/explanation/auth-model/).
