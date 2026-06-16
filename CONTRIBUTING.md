# Contributing to gotifacts

Thanks for your interest in improving gotifacts! This document mirrors what CI
enforces so you can reproduce it locally before opening a PR.

## Prerequisites

- **Go** (version pinned in [`go.mod`](go.mod))
- **Node.js** 22+ and npm (for the frontend in `web/`)
- **golangci-lint** v2.x
- Optionally **Docker** to build the runtime image

## Backend (Go)

```sh
# Run all tests with the race detector (CI: test job)
go test -race ./...

# Lint (CI: lint job)
golangci-lint run ./...

# Build the binary
go build ./cmd/gotifacts
```

The Go layout:

| Package | Responsibility |
| --- | --- |
| `cmd/gotifacts` | CLI entrypoint: `serve` and `keys` subcommands |
| `internal/config` | Env-var configuration + validation |
| `internal/store` | SQLite registry (sites + API keys), embedded migrations |
| `internal/keys` | API-key generation, hashing, capabilities |
| `internal/auth` | Two-plane authorization (forward-auth + API key) |
| `internal/archive` | Safe tar.gz extraction (zip-slip / tar-bomb defenses) |
| `internal/router` | URL ⇄ path convention + host dispatch |
| `internal/ingest` | Atomic publish, versioning, rollback |
| `internal/api` | HTTP handlers for `/api/*` and `/ingest/*` |
| `internal/portal` | Static site serving + embedded SPA |
| `web/` | Vite + React + TypeScript SPA, embedded via `go:embed` |

## Frontend (`web/`)

```sh
cd web
npm ci
npm run lint     # eslint + tsc --noEmit (CI: frontend job)
npm run build    # emits web/dist, embedded by the Go binary
npm run dev      # local dev server, proxies /api to localhost:8080
```

> **Note:** `web/dist` is gitignored (only an empty `web/dist/.gitkeep`
> placeholder is tracked, which satisfies the `go:embed` target). `go test`/`go
> build` work on a clean checkout without a frontend build; until you run `npm
> run build`, the portal HTML route returns a 500 "frontend not built" while the
> API keeps working. Run `npm run build` to serve the UI locally — there is no
> need to commit the result.

## Running a full local dev environment

In production a reverse proxy authenticates you and injects an identity header
(`Remote-User` by default); the management plane (`/api/*`, and the portal UI)
only trusts that header when the request's peer IP is in
`GOTIFACTS_TRUSTED_PROXIES`. Locally there is no such proxy, so you must (a)
trust loopback and (b) inject the header yourself — otherwise the UI shows
"reach the portal through your authenticating proxy" and the API returns
`401 authentication required`.

**1. Run the server** (in its own terminal):

```sh
GOTIFACTS_BASE_DOMAIN=localhost \
GOTIFACTS_TRUSTED_PROXIES=127.0.0.1/32,::1/128 \
GOTIFACTS_ADMIN_USERS=dev@example.com \
GOTIFACTS_DATA_DIR=./_devdata \
go run ./cmd/gotifacts serve
```

> Include **both** loopback CIDRs: `localhost` often resolves to IPv6 `::1`, so a
> v4-only trust list silently rejects the header. To create keys (the **API
> Keys** view is admin-only) the dev user must be in `GOTIFACTS_ADMIN_USERS`.

**2. Run the SPA** (second terminal). Set `GOTIFACTS_DEV_USER` to the same admin
user; the vite dev proxy then injects `Remote-User` on every `/api` call,
standing in for your auth proxy:

```sh
cd web
GOTIFACTS_DEV_USER=dev@example.com npm run dev
```

Open the printed URL. For a quick backend-only check (loopback is trusted):

```sh
curl -H "Remote-User: dev@example.com" http://127.0.0.1:8080/api/me
```

The `GOTIFACTS_DEV_USER` shim is dev-only — it has no effect on `npm run build`
or production.

## Docs

When you change behavior, update the relevant docs: `README.md`, `.env.example`,
and the proxy snippets under `examples/`.

## Commit / PR guidelines

- Keep changes focused; include tests for new behavior.
- Ensure `go test -race ./...`, `golangci-lint run`, and the frontend
  `lint`/`build` all pass.
- By contributing, you agree your contributions are licensed under the MIT
  License.
