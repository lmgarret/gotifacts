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

## A minimal working configuration

The smallest `.env` that boots a working management plane sets the canonical
domain and a reachable admin behind a trusted proxy — everything else can ride
on its default:

```sh
# Required: canonical apex domain and an admin reachable through the proxy.
GOTIFACTS_BASE_DOMAIN=example.com
GOTIFACTS_ADMIN_USERS=alice@example.com
GOTIFACTS_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12

# Optional but common: structured logs and versioned publishes.
GOTIFACTS_LOG_FORMAT=json
GOTIFACTS_VERSIONING_ENABLED=true
```

The tables below give a concrete **Example** value for every variable. Examples
are illustrative, not the defaults — where a variable has a default, it is shown
in its own column.

## Networking

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_LISTEN_ADDR` | `127.0.0.1:8080` | `:8080` | HTTP bind address (plain HTTP; TLS is the proxy's job). |
| `GOTIFACTS_BASE_DOMAIN` | `example.com` | — | **Required.** Canonical apex domain; sites are served on its sub-labels. All generated URLs (site links, OAuth issuer, `/api/me` `base_domain`) use it. |
| `GOTIFACTS_ALIAS_DOMAINS` | `example.org,example.net` | — | Comma-separated extra apex domains that serve the **same** sites as the canonical domain. Publishing once makes a site reachable under every domain; generated URLs still use `GOTIFACTS_BASE_DOMAIN`. Each domain needs its own apex + wildcard routing and TLS at your proxy. |

## Storage

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_DATA_DIR` | `/var/lib/gotifacts` | `/data` | Writable volume root: SQLite DB + all site files. |
| `GOTIFACTS_DB_PATH` | `/var/lib/gotifacts/gotifacts.db` | `${DATA_DIR}/gotifacts.db` | SQLite database path. |

## Logging

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_LOG_LEVEL` | `debug` | `info` | Minimum level emitted: `debug`, `info`, `warn`, or `error`. |
| `GOTIFACTS_LOG_FORMAT` | `json` | `text` | Log encoding: `text` (human-readable) or `json` (structured). |

Logs are written to **stdout**, so they surface in `docker logs` and Portainer
without extra configuration. The `text` format is easiest to scan in Portainer's
log view; switch to `json` when feeding a log shipper.

Each action — site publishes, deletes, rollbacks, metadata patches, API-key
creation/revocation, and MCP consent/connection changes — is logged at **info**,
alongside a one-line access record per request on the management/ingest plane.
Raise verbosity to **debug** to also see authentication decisions (why a
forward-auth or API-key request was accepted or rejected) and the high-volume
static-asset request lines from served sites.

## Authentication (management plane)

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_FORWARD_AUTH_HEADER` | `X-Forwarded-User` | `Remote-User` | Identity header injected by the proxy. |
| `GOTIFACTS_ADMIN_USERS` | `alice@example.com,bob@example.com` | — | Comma-separated forward-auth users granted admin. |
| `GOTIFACTS_TRUSTED_PROXIES` | `10.0.0.0/8,172.16.0.0/12` | — | Comma-separated CIDRs/IPs allowed to assert the identity header. **Required** for the management plane. |

The identity header is honored **only** when the request's direct peer IP is
within `GOTIFACTS_TRUSTED_PROXIES`; otherwise it is stripped. See the
[auth model](/gotifacts/explanation/auth-model/).

## Upload & extraction limits

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_MAX_UPLOAD_BYTES` | `134217728` (128 MiB) | `67108864` (64 MiB) | Max ingest request body size. |
| `GOTIFACTS_MAX_EXTRACT_BYTES` | `536870912` (512 MiB) | `268435456` (256 MiB) | Max total decompressed bytes per archive. |
| `GOTIFACTS_MAX_EXTRACT_ENTRIES` | `20000` | `10000` | Max entries extracted per archive. |

## Versioning

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_VERSIONING_ENABLED` | `true` | `false` | Retain prior versions on replace; enable rollback. |
| `GOTIFACTS_VERSIONING_KEEP` | `10` | `5` | Historical versions kept per site. |

See [enable versioning & roll back](/gotifacts/guides/versioning-and-rollback/).

## Lifecycle & retention

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_DELETED_SITE_TTL` | `168h` (7 days) | `720h` (30 days) | How long unpublished site files are kept in quarantine before permanent removal. Set to `0` to purge on the next hourly cycle. |

When a site is unpublished it is taken offline immediately and its files are moved to a quarantine directory (`$DATA_DIR/.deleted/`). A background purge job runs hourly and permanently deletes any quarantined sites whose `deleted_at` timestamp is older than this TTL. Re-publishing the same slug during the grace period restores the site from the database (quarantine files are replaced by the new publish).

## MCP connector

| Variable | Example | Default | Notes |
| --- | --- | --- | --- |
| `GOTIFACTS_MCP_ENABLED` | `true` | `false` | Expose the OAuth-protected MCP server at `/mcp`. |
| `GOTIFACTS_MCP_ALLOWED_USERS` | `alice@example.com` | — | Forward-auth users allowed to grant connector consent (falls back to `GOTIFACTS_ADMIN_USERS`). |
| `GOTIFACTS_MCP_GROUP` | `demos` | `claude` | Publish group subtree MCP tokens are confined to. |
| `GOTIFACTS_MCP_TOKEN_TTL` | `30m` | `1h` | MCP access-token lifetime (Go duration). |
| `GOTIFACTS_MCP_REFRESH_TTL` | `168h` | `720h` | MCP refresh-token lifetime (Go duration). |

See [connect Claude via MCP](/gotifacts/guides/connect-claude-mcp/).
