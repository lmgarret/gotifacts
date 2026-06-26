---
title: Deploy with Docker
description: Run gotifacts in production with Docker Compose behind your reverse proxy.
sidebar:
  order: 1
---

gotifacts ships as a single static image at `ghcr.io/lmgarret/gotifacts`. It's a
three-stage build (Node → Go → `scratch`): a static, non-root, CA-cert-only
runtime that declares `/data` as a volume.

## 1. Configure

Copy the example environment file and edit the required values:

```sh
cp .env.example .env
```

At minimum set:

- `GOTIFACTS_BASE_DOMAIN` — your apex domain.
- `GOTIFACTS_ADMIN_USERS` — comma-separated admin identities.
- `GOTIFACTS_TRUSTED_PROXIES` — CIDRs/IPs of your reverse proxy.

See the full [configuration reference](/gotifacts/reference/configuration/).

## 2. Compose file

A proxy-agnostic `docker-compose.yml` is provided in the repo. The key property:
gotifacts is reachable only on the internal network — **never publish port 8080
to the internet.**

```yaml title="docker-compose.yml"
services:
  gotifacts:
    image: ghcr.io/lmgarret/gotifacts:latest
    env_file: .env
    expose:
      - "8080"          # internal only — your proxy connects here
    volumes:
      - gotifacts-data:/data
    restart: unless-stopped

volumes:
  gotifacts-data:
```

## 3. Start

```sh
docker compose up -d
```

Then put your reverse proxy in front of it for TLS and forward-auth — see
[nginx](/gotifacts/guides/reverse-proxy-nginx/) or
[Caddy](/gotifacts/guides/reverse-proxy-caddy/).

## Image tags

| Tag | Meaning |
| --- | --- |
| `latest` | Latest released version |
| `edge` | Latest commit on `main` |
| `vMAJOR`, `vMAJOR.MINOR`, `vX.Y.Z` | Semantic version pins |

## Persistence & backups

All state lives under `/data`: the SQLite database (`gotifacts.db`) and the site
files (`sites/…`). Back up the volume to back up everything. The image runs as a
non-root user, so the volume must be writable by it.

## Running management commands

The image's entrypoint is the `gotifacts` binary, so any
[CLI subcommand](/gotifacts/reference/cli/) runs in the service container:

```sh
docker compose exec gotifacts gotifacts keys list
```

One command needs a brief maintenance window because it walks the data volume:
`migrate-layout`, the one-time upgrade that moves published files into the
`@site` layout (see the [CLI reference](/gotifacts/reference/cli/#migrate-layout)).
Run it with the server stopped so it can't race live publishes:

```sh
docker compose stop gotifacts
docker compose run --rm gotifacts migrate-layout
docker compose up -d gotifacts
```
