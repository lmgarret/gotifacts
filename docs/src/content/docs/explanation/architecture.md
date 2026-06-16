---
title: Architecture
description: How gotifacts routes requests, where state lives, and why it's shaped this way.
sidebar:
  order: 1
---

gotifacts is a single Go service that does two things: it **hosts static sites**
addressed by hostname, and it serves a **portal** to browse them. It is
deliberately small — one static binary, SQLite, and a volume.

## The big picture

```d2
direction: down

client: Client { shape: oval }
proxy: "Reverse proxy\n(operator-provided: TLS, forward-auth/SSO)"
mgmt: Portal UI + management API
ingest: Machine publish API (API key)
sites: Static site content
volume: "Volume (rw)\n/data/gotifacts.db\n/data/sites/<group>/<slug>/" { shape: cylinder }

gotifacts: "gotifacts (Go, static scratch binary) — HTTP :8080" {
  router: Host router { shape: diamond }
  apex: portal + /api + /ingest
  serve: serve site files
  router -> apex: "Host == base"
  router -> serve: "else"
}

client -> proxy
proxy -> mgmt: "apex / and /api/* — forward-auth ON"
proxy -> ingest: "apex /ingest/* — forward-auth OFF"
proxy -> sites: "*.base, *.*.base"
mgmt -> gotifacts.router
ingest -> gotifacts.router
sites -> gotifacts.router
gotifacts -> volume
```

## Host-based routing

The service routes purely by the request `Host`:

- **Apex host** (`== GOTIFACTS_BASE_DOMAIN`): serves the portal SPA, the
  management API (`/api/*`), and the ingest API (`/ingest/*`).
- **Any other host**: maps the host to a site directory under `/data/sites/…`
  and serves static files.

The host→directory mapping is the [URL ⇄ path
convention](/gotifacts/reference/url-path-convention/). This is why no domains
are hardcoded: the apex is configured, and everything else is derived from it.

## Why a reverse proxy is mandatory

gotifacts serves **plain HTTP on one port and never TLS**. It deliberately does
*not* implement TLS termination, certificate management, or SSO. Those are
solved problems that your proxy already does well, and keeping them out lets
gotifacts stay a tiny, auditable binary. The proxy provides TLS and forward-auth;
gotifacts enforces its own authorization on top. See the
[auth model](/gotifacts/explanation/auth-model/).

## State

The only state is the volume:

- `gotifacts.db` — a SQLite registry (via the pure-Go `modernc.org/sqlite`
  driver, so the binary stays CGO-free) holding site metadata and API keys.
- `sites/<group…>/<slug>/` — the published files for each site.

Publishing writes to a temp dir on the same volume, validates the upload, then
**atomically swaps** it into place — so a site is never served half-written.

## Why this shape

- **One static binary** (`FROM scratch`) is trivial to deploy and audit, and has
  a minimal attack surface.
- **SQLite + a volume** means no external database to operate; back up the volume
  and you've backed up everything.
- **Configuration only via environment** keeps deployments reproducible and
  twelve-factor friendly.
