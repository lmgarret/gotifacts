---
title: Configuration
description: Every gotifacts environment variable, its default, and what it controls.
sidebar:
  order: 1
---

gotifacts is configured **entirely through environment variables** — there is no
config file. The canonical annotated reference is
[`.env.example`](https://github.com/lmgarret/gotifacts/blob/main/.env.example) in
the repo.

`gotifacts serve` refuses to start if no admins are reachable (no
`GOTIFACTS_ADMIN_USERS` + `GOTIFACTS_TRUSTED_PROXIES`); use the
[CLI](/gotifacts/reference/cli/) to mint an admin key for purely headless setups.

## Networking

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_LISTEN_ADDR` | `:8080` | HTTP bind address (plain HTTP; TLS is the proxy's job). |
| `GOTIFACTS_BASE_DOMAIN` | — | **Required.** Apex domain; sites are served on its sub-labels. |

## Storage

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_DATA_DIR` | `/data` | Writable volume root: SQLite DB + all site files. |
| `GOTIFACTS_DB_PATH` | `${DATA_DIR}/gotifacts.db` | SQLite database path. |

## Authentication (management plane)

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_FORWARD_AUTH_HEADER` | `Remote-User` | Identity header injected by the proxy. |
| `GOTIFACTS_ADMIN_USERS` | — | Comma-separated forward-auth users granted admin. |
| `GOTIFACTS_TRUSTED_PROXIES` | — | Comma-separated CIDRs/IPs allowed to assert the identity header. **Required** for the management plane. |

The identity header is honored **only** when the request's direct peer IP is
within `GOTIFACTS_TRUSTED_PROXIES`; otherwise it is stripped. See the
[auth model](/gotifacts/explanation/auth-model/).

## Upload & extraction limits

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_MAX_UPLOAD_BYTES` | `67108864` (64 MiB) | Max ingest request body size. |
| `GOTIFACTS_MAX_EXTRACT_BYTES` | `268435456` (256 MiB) | Max total decompressed bytes per archive. |
| `GOTIFACTS_MAX_EXTRACT_ENTRIES` | `10000` | Max entries extracted per archive. |

## Versioning

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_VERSIONING_ENABLED` | `false` | Retain prior versions on replace; enable rollback. |
| `GOTIFACTS_VERSIONING_KEEP` | `5` | Historical versions kept per site. |

See [enable versioning & roll back](/gotifacts/guides/versioning-and-rollback/).

## Lifecycle & retention

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_DELETED_SITE_TTL` | `720h` (30 days) | How long unpublished site files are kept in quarantine before permanent removal. Set to `0` to purge on the next hourly cycle. |

When a site is unpublished it is taken offline immediately and its files are moved to a quarantine directory (`$DATA_DIR/.deleted/`). A background purge job runs hourly and permanently deletes any quarantined sites whose `deleted_at` timestamp is older than this TTL. Re-publishing the same slug during the grace period restores the site from the database (quarantine files are replaced by the new publish).

## MCP connector

| Variable | Default | Notes |
| --- | --- | --- |
| `GOTIFACTS_MCP_ENABLED` | `false` | Expose the OAuth-protected MCP server at `/mcp`. |
| `GOTIFACTS_MCP_ALLOWED_USERS` | — | Forward-auth users allowed to grant connector consent (falls back to `GOTIFACTS_ADMIN_USERS`). |
| `GOTIFACTS_MCP_GROUP` | `claude` | Publish group subtree MCP tokens are confined to. |
| `GOTIFACTS_MCP_TOKEN_TTL` | `1h` | MCP access-token lifetime (Go duration). |
| `GOTIFACTS_MCP_REFRESH_TTL` | `720h` | MCP refresh-token lifetime (Go duration). |

See [connect Claude via MCP](/gotifacts/guides/connect-claude-mcp/).
