# gotifacts

[![CI](https://github.com/lmgarret/gotifacts/actions/workflows/ci.yml/badge.svg)](https://github.com/lmgarret/gotifacts/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/lmgarret/gotifacts)](https://goreportcard.com/report/github.com/lmgarret/gotifacts)

A single, self-hosted **Go** service that **hosts static sites** by host-based
routing and serves a **dynamic portal** to browse them. You publish sites over
an HTTP API; gotifacts stores them on a volume with a **SQLite** registry and
serves them at `https://<slug>.<group>.<base>`.

gotifacts runs behind **any** reverse proxy you provide (nginx, Caddy, …) for
TLS and SSO/forward-auth. It serves plain HTTP on one port, never TLS, and
enforces its own authorization.

- **One static binary** (CGO-free, built `FROM scratch`).
- **SQLite + a volume** are the only state.
- **No hardcoded domains/hosts/paths** — everything is configurable.

---

## Table of contents

- [How it works](#how-it-works)
- [Two-plane auth model](#two-plane-auth-model)
- [URL ⇄ path convention](#url--path-convention)
- [Scopes & API keys](#scopes--api-keys)
- [API reference](#api-reference)
- [Configuration](#configuration)
- [Running with Docker](#running-with-docker)
- [Reverse proxy setup](#reverse-proxy-setup)
- [Security](#security)
- [Publishing from CI or Claude](#publishing-from-ci-or-claude)
- [Development](#development)

---

## How it works

```
reverse proxy (operator-provided: TLS, forward-auth/SSO)
  ├── apex "/" and "/api/*"   → forward-auth ON  → portal UI + management API
  ├── apex "/ingest/*"        → forward-auth OFF → machine publish API (API-key)
  └── *.base, *.*.base        → static site content
                                      │  HTTP :8080
                                      ▼
                 ┌──────────────────────────────────────────┐
                 │  gotifacts (Go, static scratch binary)     │
                 │  Host router:                              │
                 │   Host == base → portal + /api + /ingest   │
                 │   else         → serve site files          │
                 │  Registry + API keys: SQLite (modernc)     │
                 └──────────────┬─────────────────────────────┘
                                ▼ volume (rw)
        /data/gotifacts.db  +  /data/sites/<group…>/<slug>/{index.html, assets…}
```

The service routes purely by the request `Host`:

- **Apex host** (`== GOTIFACTS_BASE_DOMAIN`): serves the portal SPA, the
  management API (`/api/*`), and the ingest API (`/ingest/*`).
- **Any other host**: maps the host to a site directory and serves static files.

## Two-plane auth model

gotifacts has two independent authorization planes:

| Plane | Routes | Authenticated by | Used by |
| --- | --- | --- | --- |
| **Management** | `/`, `/api/*` | a **forward-auth identity header** injected by your proxy | the browser (portal) |
| **Ingest** | `/ingest/*` | a scoped **API key** (`Authorization: Bearer <key>`) | machines (CI, the Claude skill) |

- The identity header (`GOTIFACTS_FORWARD_AUTH_HEADER`, default `Remote-User`)
  is honored **only** when the request's direct peer IP is within
  `GOTIFACTS_TRUSTED_PROXIES`. From any other source it is stripped and ignored.
- The principal is that user; they are **admin** iff they are listed in
  `GOTIFACTS_ADMIN_USERS`.
- On the ingest plane the identity header is irrelevant — only the API key
  counts. Leave `/ingest/*` **out** of your proxy's forward-auth.

**No API key ever lives in the browser.** The portal authenticates you purely
via the proxy-injected header.

## URL ⇄ path convention

Strip `base_domain` from a host. The remaining sub-labels, read left→right, run
`[most-specific … least-specific]`. The served directory is those labels
**reversed**:

| Host | Served directory | `group` | `slug` |
| --- | --- | --- | --- |
| `app.claude.<base>` | `sites/claude/app` | `claude` | `app` |
| `a.sub.grp.<base>` | `sites/grp/sub/a` | `grp/sub` | `a` |
| `demo.<base>` | `sites/demo` | *(flat)* | `demo` |

Rules:

- **Total depth (group segments + slug) ≤ 3.** Deeper hosts are rejected.
- Each label must match `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`.
- A site is identified on publish by `group` (0–2 segments, may be empty) +
  `slug` (the leaf).

## Scopes & API keys

| Role | Granted by | Can do |
| --- | --- | --- |
| **Admin** | forward-auth allowlist **or** an `admin`-scoped key | everything: manage keys, delete/patch/rollback sites, publish anywhere |
| **Publish** | a `publish`-scoped key | publish only (`POST /ingest/sites`), confined to `group_restriction` if set |
| **Viewer** | any authenticated forward-auth user | view the portal and `GET /api/sites` |

API keys:

- Token format `gtf_<base64url-32B>`, shown in plaintext **once** at creation.
- Only the **SHA-256 hash** is stored; lookups are constant-time.
- Mint them in the portal (**API Keys** view, admin only) or via the CLI:

  ```sh
  gotifacts keys create --name ci --scope publish --group claude
  gotifacts keys list
  gotifacts keys revoke --id 3
  ```

There is **no bootstrap key**: set `GOTIFACTS_ADMIN_USERS`, log in through your
proxy, and create keys in the UI (the CLI is the headless fallback).

## API reference

### Management plane — `/api/*` (forward-auth)

| Method & path | Scope | Description |
| --- | --- | --- |
| `GET /api/me` | viewer+ | `{ user, is_admin, base_domain }` |
| `GET /api/sites` | viewer+ | Flat list **and** nested group tree. Query: `q`, `tag`, `group`, `sort` (`date`\|`title`\|`slug`), `hidden=true` (admin), `limit`, `offset` |
| `POST /api/sites` | admin | Manual upload (same multipart body as ingest) |
| `PATCH /api/sites/{group…}/{slug}` | admin | Metadata-only update |
| `DELETE /api/sites/{group…}/{slug}` | admin | Delete site + files |
| `POST /api/sites/{group…}/{slug}/rollback` | admin | Restore the latest archived version |
| `GET /api/keys` | admin | List keys (no plaintext) |
| `POST /api/keys` | admin | `{name, scope, group?}` → `201 {id, name, scope, group_restriction, key}` (plaintext **once**) |
| `DELETE /api/keys/{id}` | admin | Revoke a key |

### Ingest plane — `/ingest/*` (API key)

**`POST /ingest/sites`** — create or replace a site. `multipart/form-data`:

- `meta` — JSON: `{group, slug, title, description?, date?, tags?, repo?, preview?, hidden?}`
- **either** `bundle` — a `.tar.gz` containing a top-level `index.html`
- **or** `index` — a single self-contained HTML document (becomes `index.html`)

Requires `publish` or `admin` scope; `group_restriction` is enforced.
Idempotent (same `group`/`slug` replaces). Response:

```json
{ "url": "https://app.claude.example.com", "group": "claude", "slug": "app", "updated_at": "2026-06-04T..." }
```

**`DELETE /ingest/sites/{group…}/{slug}`** — admin-scoped key only (automation
cleanup).

Example publish of a single HTML file:

```sh
printf '{"group":"claude","slug":"app","title":"My App"}' > meta.json
curl -fsS \
  -H "Authorization: Bearer $GOTIFACTS_API_KEY" \
  -F 'meta=<meta.json;type=application/json' \
  -F 'index=@index.html;type=text/html' \
  https://example.com/ingest/sites
```

## Configuration

All configuration is via environment variables (no config file). See
[`.env.example`](.env.example) for the annotated reference.

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_LISTEN_ADDR` | `:8080` | HTTP bind address |
| `GOTIFACTS_DATA_DIR` | `/data` | Volume root (DB + site files) |
| `GOTIFACTS_DB_PATH` | `${DATA_DIR}/gotifacts.db` | SQLite path |
| `GOTIFACTS_BASE_DOMAIN` | — | **Required.** Apex domain |
| `GOTIFACTS_FORWARD_AUTH_HEADER` | `Remote-User` | Identity header from the proxy |
| `GOTIFACTS_ADMIN_USERS` | — | Comma-separated admin users |
| `GOTIFACTS_TRUSTED_PROXIES` | — | Comma-separated CIDRs/IPs allowed to assert the identity header (**required** for the management plane) |
| `GOTIFACTS_MAX_UPLOAD_BYTES` | `67108864` (64 MiB) | Ingest body cap |
| `GOTIFACTS_MAX_EXTRACT_BYTES` | `268435456` (256 MiB) | Decompressed archive cap |
| `GOTIFACTS_MAX_EXTRACT_ENTRIES` | `10000` | Archive entry cap |
| `GOTIFACTS_VERSIONING_ENABLED` | `false` | Keep old versions on replace; enable rollback |
| `GOTIFACTS_VERSIONING_KEEP` | `5` | Versions retained per site |
| `GOTIFACTS_MCP_ENABLED` | `false` | Expose the OAuth-protected MCP server at `/mcp` |
| `GOTIFACTS_MCP_ALLOWED_USERS` | — | Forward-auth users allowed to grant connector consent (falls back to `GOTIFACTS_ADMIN_USERS`) |
| `GOTIFACTS_MCP_GROUP` | `claude` | Publish group subtree MCP tokens are confined to |
| `GOTIFACTS_MCP_TOKEN_TTL` | `1h` | MCP access-token lifetime |
| `GOTIFACTS_MCP_REFRESH_TTL` | `720h` | MCP refresh-token lifetime |

`gotifacts serve` refuses to start if no admins are reachable (no
`GOTIFACTS_ADMIN_USERS` + `GOTIFACTS_TRUSTED_PROXIES`); use the CLI to mint an
admin key for purely headless setups.

## Running with Docker

A proxy-agnostic [`docker-compose.yml`](docker-compose.yml) is provided. The
image is published to `ghcr.io/lmgarret/gotifacts`.

```sh
cp .env.example .env
# edit .env: set GOTIFACTS_BASE_DOMAIN, GOTIFACTS_ADMIN_USERS, GOTIFACTS_TRUSTED_PROXIES
docker compose up -d
```

gotifacts is reachable only on the internal network (`expose: 8080`). Put your
reverse proxy in front of it — **never expose port 8080 to the internet.**

The image is a three-stage build (Node → Go → `scratch`): a static, non-root,
CA-cert-only runtime that declares `/data` as a volume.

## Reverse proxy setup

gotifacts is proxy-agnostic. Reference snippets (illustrative, adapt to your
SSO provider):

- **nginx** — [`examples/nginx/gotifacts.conf`](examples/nginx/gotifacts.conf)
- **Caddy** — [`examples/caddy/Caddyfile`](examples/caddy/Caddyfile)

Each shows: TLS; apex `/` + `/api/*` behind forward-auth with the identity
header injected (and any client-supplied copy stripped); apex `/ingest/*` with
forward-auth **off**; and `*.base` / `*.*.base` serving site content.

### Framing sites in the portal

The portal renders **live, sandboxed iframe thumbnails** of sites. For a site
to be framable by the portal, it must be served with:

```
Content-Security-Policy: frame-ancestors https://<base>
```

The proxy examples add this header to site responses. If you set `preview` in a
site's metadata, the portal uses that image instead of an iframe.

## Security

See [`SECURITY.md`](SECURITY.md) for the threat model and private reporting.
Highlights:

- **Identity-header spoofing is the top risk.** The header is honored only from
  `GOTIFACTS_TRUSTED_PROXIES`; otherwise stripped. Never expose gotifacts
  directly; your proxy must strip any client-supplied identity header before
  injecting the real one.
- **Uploads** are guarded against zip-slip, symlink escapes, tar-bombs, and
  oversized payloads. Sites are written to a temp dir on the same volume,
  validated, then atomically swapped into place.
- **API keys** are hashed at rest, shown once, compared in constant time, and
  never logged.

## Publishing from CI or Claude

There are two ways to let Claude publish, suited to different surfaces.

### Skill + API key (CI, Claude Code, the API code-execution tool)

A distributable **Claude skill** lives in
[`examples/skill/`](examples/skill/SKILL.md). It asks for consent, writes a
self-contained `index.html`, picks a URL-safe `slug`/`group` (default
`claude`), publishes via the single-`index` ingest form using `GOTIFACTS_URL` +
a `publish`-scoped `GOTIFACTS_API_KEY`, and reports the URL. It never touches
server/proxy credentials.

This works wherever **you** control the environment (CI, Claude Code, the API
code-execution tool). It does **not** work in default claude.ai / Claude mobile
conversations, which provide no way to inject those environment variables.

### MCP connector + OAuth (claude.ai mobile/web, Claude Code, the API)

For the consumer apps, set `GOTIFACTS_MCP_ENABLED=true` to expose an OAuth
2.1-protected [Model Context Protocol](https://modelcontextprotocol.io) server
at `/mcp` with a single `publish_site` tool. Claude's "custom connector" UI
authenticates remote MCP servers exclusively via OAuth (no bearer/header
field), so this is the only path that works on mobile/web.

How it fits the two-plane model:

- The browser-facing **consent** step (`/mcp/oauth/authorize`) is authenticated
  by your existing forward-auth SSO — it sits on the **forward-auth plane** and
  is gated to `GOTIFACTS_MCP_ALLOWED_USERS` (or admins). This is the gate that
  decides *who* may publish.
- Every machine-facing endpoint (`/mcp`, the token/registration endpoints, and
  the `/.well-known/oauth-*` discovery documents) is called server-to-server by
  Claude and must sit on the **no-forward-auth plane**, like `/ingest/*`.
  Authentication there is OAuth (PKCE, then a bearer access token).

Tokens are `publish`-scoped and confined to `GOTIFACTS_MCP_GROUP` (default
`claude`), so a connector can never publish outside that subtree. Dynamic Client
Registration (RFC 7591) lets users add the connector by pasting the URL; it only
issues a `client_id` and grants no access on its own. See the proxy examples for
the exact routing split, and `.env.example` for all MCP settings.

To connect: in Claude → Settings → Connectors → *Add custom connector*, enter
`https://<your-base-domain>/mcp`, complete the SSO consent, then ask Claude to
publish a page. For the **API MCP connector** or **Claude Code**, the same
server works with a token obtained via the OAuth flow.

## Development

See [`CONTRIBUTING.md`](CONTRIBUTING.md). In short:

```sh
go test -race ./...          # backend tests
golangci-lint run ./...      # backend lint
cd web && npm ci && npm run lint && npm run build   # frontend
```

The Go module path is `github.com/lmgarret/gotifacts`; `go.mod` is the single
source of truth for the Go version.

## License

[MIT](LICENSE).
