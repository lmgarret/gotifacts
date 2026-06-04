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
| `internal/keys` | API-key generation, hashing, scopes |
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

> **Note:** `web/dist` is committed so that `go test`/`go build` always have an
> embed target. After changing the frontend, run `npm run build` and commit the
> regenerated `web/dist`.

## Docs

When you change behavior, update the relevant docs: `README.md`, `.env.example`,
and the proxy snippets under `examples/`.

## Commit / PR guidelines

- Keep changes focused; include tests for new behavior.
- Ensure `go test -race ./...`, `golangci-lint run`, and the frontend
  `lint`/`build` all pass.
- By contributing, you agree your contributions are licensed under the MIT
  License.
