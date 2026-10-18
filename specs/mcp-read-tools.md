# Spec: read-only MCP tools for browsing published sites

Status: draft · Not implemented

## Problem

Many sites published on gotifacts sit behind the reverse proxy's forward-auth,
and some of them serve LLM-friendly docs (`llms.txt`, `llms-full.txt`,
Markdown). An agent can't reach them: its HTTP fetch has no SSO session, and
the read endpoints under `GET /api/sites/...` accept forward-auth users only.

The MCP endpoint (`/mcp`) already authenticates with its own OAuth and is
deliberately kept out of forward-auth, and gotifacts reads site content directly
from disk. Read-only MCP tools can therefore serve that content to an
authorized agent without going through the proxy.

## Goals

- An agent connected over MCP can discover sites, list their files, read text
  files and search within a site.
- `llms.txt` is the first-class entry point.
- Access goes through the existing grant model and is never wider than what the
  connection was granted.

## Non-goals

- Converting HTML to Markdown, or rendering pages. v1 returns raw text.
- Reading archived revisions or soft-deleted sites. v1 reads the live
  (current) revision only.
- Read endpoints on the ingest plane (`/ingest`) for API keys.
- Exposing content as MCP *resources*. Clients support resources less well than
  tools; this can be revisited later.
- Any change to the HTTP API. `docs/openapi.yaml` only changes because of the
  new capability enum value (see below).

## Authorization

### New capability: `read`

- Add `keys.CapRead Capability = "read"` in `internal/keys/keys.go`, and accept
  it in `Valid()`.
- Grants are stored as a CSV `TEXT` column (`api_key_grants.permissions`), so
  **no migration is needed**.
- Every new tool checks
  `p.Can(keys.CapRead, group, slug)` for the target site, the same way the
  existing tools check their own capability. Admin principals pass, as today.
- `list_sites` filters its results to the sites where `p.Can(keys.CapRead, ...)`
  is true.
- API keys may also hold `read` (the keys API and portal accept it), but no
  ingest endpoint uses it in v1. The UI description must say it applies to MCP
  connectors.

### Consent screen: grant editor in the portal (revision 2)

The first version added a separate "Read access" section to the Go-rendered
consent page. In practice the read target was easy to fill in while leaving its
checkbox unticked, which silently produced no read grant. It also could not
express several grants or "all sites", unlike the API keys screen. Revision 2
replaces it: **the consent screen is rendered by the React portal and uses the
same grant editor as the API keys screen.**

What the user sees, at `/mcp/oauth/authorize?...` (URL unchanged, so OAuth
clients need no change):

- A standalone, centred card with no portal navigation: the logo, the signed-in
  user, and "**<client name>** wants to access sites on `<base domain>`".
- A list of grant cards, the same component as the API keys screen
  (`GrantCard` + `TargetSelect`): scope **group / site / global (all sites)**, a
  target with suggestions from existing groups and sites (`GET /api/sites`), and
  the permission checkboxes for **all six capabilities, `read` included**.
  "+ Add grant" and per-grant remove (when more than one).
- One default grant, expanded: group `GOTIFACTS_MCP_GROUP` with `publish`
  (same default as today).
- A global grant shows the same warning and "I understand the risk" checkbox
  as the API keys screen; approval is blocked until it is ticked.
- Client-side validation mirrors the keys screen: at least one grant, each
  group/site grant needs a target, each grant needs at least one permission.
- **Approve** and **Deny** buttons. Usable at ~375px.

Implementation:

- **Shared editor.** Extract the grants list (cards, add/remove, global
  warning + acknowledgement, validation) from `KeyCreateModal.tsx` into a
  reusable component, e.g. `web/src/components/GrantsEditor.tsx`, used by both
  the API keys modal and the consent view. The keys modal must behave exactly
  as before (its admin toggle stays in the modal).
- **Routing.** `GET /mcp/oauth/authorize` keeps running
  `validateAuthRequest` (with the `MCPUserAllowed` check). Errors are reported
  as today: an HTML/plain error before the redirect URI is trusted, an OAuth
  error redirect after. On success it serves the SPA's `index.html` (the api
  server's `s.spa` handler) instead of the Go template. The SPA router gets a
  `consent` view for that path that reads the OAuth parameters from the query
  string. Remove `consentTmpl`, `consentData`, `consentCap` and the form-POST
  handling of `POST /mcp/oauth/authorize` (drop that route).
- **New management-plane endpoints** (forward-auth, `requireUser`, plus
  `MCPUserAllowed` → 403 otherwise), implemented in `internal/mcpserver` and
  registered in `internal/api/api.go` next to the other MCP routes:
  - `GET /api/mcp/consent?client_id&redirect_uri&response_type&code_challenge&code_challenge_method&state`
    validates the request the same way and returns
    `{client_name, user, base_domain, default_target, capabilities, csrf}`
    (`capabilities` = the capabilities the consent screen offers, in display
    order). Invalid params → 400 JSON `{error}`; no redirects from this
    endpoint.
  - `POST /api/mcp/consent` with JSON
    `{client_id, redirect_uri, code_challenge, code_challenge_method, state, csrf, action: "approve"|"deny", grants: [{kind, target, permissions}], acknowledge_global: bool}`.
    It checks CSRF, re-validates the request, and answers `200 {redirect_url}`.
    The URL carries `code` (approve) or `error=access_denied` (deny) or
    `error=invalid_scope&error_description=...` (bad grants), like the
    redirects today. Errors before the redirect URI is trusted (unknown client,
    bad redirect URI) → 400 JSON. The SPA then does
    `window.location.assign(redirect_url)`.
- **Grant validation** (server-side, authoritative): 1–16 grants; each `kind`
  is `group` or `site`; target normalised and checked with `normalizeTarget` /
  `validateGrantTarget`; permissions non-empty, deduplicated, each one of
  `consentCapabilities`, which now **includes `read`** (`publish` stays the
  default); a global grant (group kind, empty target) requires
  `acknowledge_global: true`, otherwise `invalid_scope` "acknowledge the
  global-scope risk". Remove `parseConsentGrants`'s `read_enabled` /
  `read_target*` handling.
- Storage, token exchange and `Principal.Can` already handle multiple grants;
  log one "MCP consent granted" line per grant.
- `docs/openapi.yaml`: document `GET` and `POST /api/mcp/consent` with their
  schemas (reuse `Grant`). `/mcp/oauth/authorize` is not in the spec today;
  leave it out.

### Hidden sites

"Hidden" only removes a site from the portal listing for non-admins; the site
is still served at its URL. For MCP:

- A `read` grant that covers a hidden site can read it, like any other site.
- `list_sites` leaves hidden sites out by default, mirroring the portal, and
  includes them when called with `include_hidden: true`. Each listed site
  carries a `hidden` flag.

### Not-found vs forbidden

For any site the caller can't read (no grant, deleted, or missing), return one
uniform error: `site "<slug>" in group "<group>" not found or not readable by
this connection`.

## Tools

All four tools are registered in `internal/mcpserver/mcpserver.go`, next to the
existing ones, with `Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true,
IdempotentHint: true, OpenWorldHint: ptr(false)}` (check the field types against
go-sdk v1.8.0).

`group` follows the existing tools' convention: when empty it defaults to
`cfg.MCPGroup`. Tool descriptions must say so, and must tell the agent to pass
the `group` returned by `list_sites`. Known limitation, shared with the existing
tools and out of scope here: sites with no group (`""`, e.g. `docs.<base>`)
can't be addressed by `list_files` / `read_file` / `search_site`. `list_sites`
still lists them.

Each tool returns both structured output (typed `Out` struct) and a short
`TextContent` summary, like the existing tools. Errors use `errorResult(...)`.

### 1. `list_sites`

Discover readable sites.

Input:

| Field | Type | Notes |
| --- | --- | --- |
| `query` | string, optional | substring on slug/title/description (`store.ListFilter.Query`) |
| `tag` | string, optional | exact tag |
| `group` | string, optional | group subtree; **no** `MCPGroup` default here (empty means all) |
| `include_hidden` | bool, optional | default false |
| `limit` | int, optional | default 50, max 200 |
| `offset` | int, optional | default 0 |

Behaviour: call `store.ListSites` with `IncludeHide: in.IncludeHidden`, keep
only the sites the caller may read, then page in memory. Site counts are small
enough for that.

Output: `{ sites: [...], total: int }`, where each site is:

```json
{ "group": "docs", "slug": "app", "url": "https://app.docs.example.com/",
  "title": "...", "description": "...", "tags": ["..."],
  "updated_at": "RFC3339", "hidden": false,
  "llms_txt": true, "llms_full_txt": false }
```

`llms_txt` / `llms_full_txt` come from stat-ing those files at the root of the
live content dir. The text summary is one line per site:
`group/slug — title (llms.txt)`.

### 2. `list_files`

List a site's files in the live revision.

Input: `slug` (required), `group`, `prefix` (optional subdirectory, e.g.
`docs/`), `limit` (default 500, max 2000).

Output: `{ files: [{path, size}], total: int, truncated: bool }`. This is a
flat list sorted by path, directories omitted. A flat list is easier for an
agent to use than the nested `FileNode` tree.

### 3. `read_file`

Read one text file from the live revision.

Input:

| Field | Type | Notes |
| --- | --- | --- |
| `slug` | string | required |
| `group` | string | optional |
| `path` | string | optional; see resolution below |
| `offset` | int | byte offset, default 0 |
| `limit` | int | bytes, default 65536 (64 KiB), max 262144 (256 KiB) |

Path resolution:

- When empty: `llms.txt` if present, else `index.html`.
- A path ending in `/` (or naming a directory): `<path>index.html`.
- Leading `/` is allowed and ignored (so a URL path can be pasted as is).
- Resolve with the existing traversal-safe join (`safeJoin`). Anything outside
  the site root, or a symlink, is not found. Ingest already rejects symlinks;
  this is defence in depth.

Text-only: sniff the first 512 bytes (`http.DetectContentType`) and check
`utf8.Valid` on the returned slice. Reject binary content with
`"<path> is a binary file (<content-type>); read_file returns text only"`.
When the slice boundary splits a multi-byte rune, move the end back to the last
complete rune so the returned text is always valid UTF-8, and set
`next_offset` accordingly.

Output:

```json
{ "path": "llms.txt", "content_type": "text/plain; charset=utf-8",
  "size": 120345, "offset": 0, "next_offset": 65536, "truncated": true,
  "content": "..." }
```

The `TextContent` is the file content itself. When `truncated`, it ends with a
one-line footer: `[truncated: call read_file with offset=<next_offset> to
continue]`.

### 4. `search_site`

Search a site's text files. It saves the agent from reading
`llms-full.txt` end to end.

Input: `slug` (required), `group`, `query` (required, plain case-insensitive
substring, no regex), `prefix` (optional subdirectory), `max_results` (default
50, max 200).

Behaviour: walk the live revision. Skip binary files (same sniff as
`read_file`) and files over 2 MiB. Match line by line.

Output: `{ matches: [{path, line, text}], truncated: bool }`. `line` is
1-based; `text` is the matching line, trimmed to 300 chars. The text summary
uses `path:line: text` per match.

## Refactor needed

`buildFileTree` and `safeJoin` live in `internal/api/files.go`, and
`internal/api` imports `internal/mcpserver`, so mcpserver can't import api. Move
the shared filesystem helpers into a new small package, e.g.
`internal/sitefs`:

- `SafeJoin(root, rel string) (string, error)`, moved as is.
- `Walk(root, prefix string, fn func(rel string, size int64) error) error`, a
  sorted file walk that skips directories and symlinks.
- `IsText(head []byte) (contentType string, ok bool)`.

`internal/api` then uses `sitefs.SafeJoin`, and `buildFileTree` can stay in api
(built on `sitefs.Walk` or unchanged). The live directory comes from
`pub.RevisionDir(sp, ingest.CurrentRevision)`; site lookup uses
`router.NewSitePath` + `store.GetSite`, as in `handleSiteGet`.

Put the tool handlers in a new file, `internal/mcpserver/read.go`, to keep
`mcpserver.go` from growing further.

## Logging

Log each read at debug level through `s.reqLog(req)`: user, tool, group, slug,
path. Don't log content. Log denials at info, mirroring the existing tools.

## Security notes

- Content returned by these tools enters the agent's context. A site carrying
  prompt-injection text can steer the agent. Document this in the security
  explanation page; there's no code mitigation in v1.
- These tools widen what an MCP token can do. A token without `read` must see
  no change in behaviour, and tests must cover that.

## Web UI

- `web/src/api.ts`: add `"read"` to the `Capability` type and to
  `CAPABILITIES`.
- `web/src/grants.ts`: add a description, e.g. `read: { desc: "Read site files
  over MCP (list, read, search). Not used by the ingest API." }`.
- Check that the grant editor still fits at ~375px with six checkboxes.

## Docs (same change, per AGENTS.md)

- `docs/openapi.yaml`: add `read` to the capability enum (around line 756).
- `docs/src/content/docs/reference/permissions.md`: add a `read` row to the
  capabilities table, the hidden-site behaviour, and the fact that an MCP
  connection can carry a separate read grant.
- `docs/src/content/docs/guides/connect-claude-mcp.md`: describe the new tools,
  the "Read access" section of the consent screen, and `llms.txt` as the entry
  point.
- `docs/src/content/docs/explanation/security.md`: add the prompt-injection
  note.
- `internal/mcpserver/mcpserver.go`: update the package doc comment, which
  still says "a single publish_site tool".
- Verify with `cd docs && npm run build`.

## Tests

In `internal/mcpserver` (follow `newTestService` / existing tool tests):

- Authorization: no `read` → every tool refuses. A `read` group grant on
  `docs` → reads `docs/*` and is refused on `claude/*`. A site grant → only
  that site. Admin → everything.
- Hidden: a covering `read` grant can read a hidden site; `list_sites` omits it
  by default and includes it (with `hidden: true`) when `include_hidden` is set.
- Not-found: no grant, deleted and missing sites all return the same error
  text.
- Consent (revision 2): `GET /mcp/oauth/authorize` serves the SPA on valid
  params and keeps today's error behaviour on invalid ones; non-allowed user →
  403. `GET /api/mcp/consent` returns client name, default target, offered
  capabilities (including `read`) and a CSRF token; invalid params → 400.
  `POST /api/mcp/consent`: bad CSRF → 400; deny → `access_denied` redirect URL;
  several grants (e.g. publish on `claude` + read on `docs`) → a token that can
  publish to the first and only read the second; global grant without
  `acknowledge_global` → `invalid_scope`, with it → allowed; empty grants, an
  empty permission list, an unknown capability, a bad target or more than 16
  grants → `invalid_scope`. Migrate `TestOAuthFlow` and the consent helpers to
  the JSON endpoint.
- `list_sites`: filtering, paging, `llms_txt` flags.
- `read_file`: default `llms.txt`, falling back to `index.html`; directory
  path → `index.html`; leading `/`; traversal (`../`, encoded segments) → not
  found; binary → refused; paging across a multi-byte rune boundary yields
  valid UTF-8 and a correct `next_offset`; `limit` is clamped.
- `list_files`: prefix, truncation.
- `search_site`: case-insensitivity, skipping binary/oversized files,
  `max_results` truncation.
- `internal/keys`: `ParseCapability("read")` is accepted.
- `internal/sitefs`: unit tests moved/added for `SafeJoin`, `Walk`, `IsText`.

Run before committing: `go test -race ./...`, `golangci-lint run ./...`,
`cd web && npm run lint && npm run build`, `cd docs && npm run build`.

## Suggested commits

1. `refactor: extract site filesystem helpers into internal/sitefs`
2. `feat(keys): add read capability` (plus web UI, openapi enum, permissions
   doc)
3. `feat(mcp): add separate read target to the consent screen`
4. `feat(mcp): add read-only site browsing tools` (tools, tests, guide and
   security docs)

## Decisions

- Hidden sites: readable under any covering `read` grant; `list_sites` omits
  them unless `include_hidden` is set.
- Consent (revision 2): the consent screen is a portal page using the API keys
  grant editor: several grants, group/site/global scopes, and `read` as a
  regular per-grant permission. It replaces the separate "Read access"
  section.
- `group` default: the read tools keep the existing tools' behaviour. Fixing
  access to sites with no group is a separate change.
