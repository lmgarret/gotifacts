---
title: Run gotifacts locally
description: Get a working gotifacts instance running behind Caddy on your machine, then log in to the portal.
sidebar:
  order: 1
---

By the end of this tutorial you'll have gotifacts running on your own machine,
behind a reverse proxy, and you'll be logged in to the portal as an admin. It
takes about ten minutes and assumes only that you have **Docker** (with the
Compose plugin) installed.

:::note
This is a *local learning setup*. It fakes the SSO layer with a single trusted
header so you can see the whole system work end to end. For a real deployment,
follow the how-to guides on [Docker](/gotifacts/guides/deploy-with-docker/) and
a [reverse proxy](/gotifacts/guides/reverse-proxy-caddy/).
:::

## Step 1 — Create a working directory

```sh
mkdir gotifacts-demo && cd gotifacts-demo
```

## Step 2 — Configure the instance

gotifacts is configured entirely through environment variables. Create a `.env`
file:

```ini title=".env"
GOTIFACTS_BASE_DOMAIN=gotifacts.localhost
GOTIFACTS_ADMIN_USERS=you@example.com
# Trust the Docker network so our proxy may assert the identity header.
GOTIFACTS_TRUSTED_PROXIES=172.16.0.0/12
```

`GOTIFACTS_ADMIN_USERS` is the list of forward-auth users who get admin rights —
use any string here; we'll send it as our identity in the next step.

## Step 3 — Define the services

We run gotifacts plus a tiny Caddy proxy. Caddy terminates HTTP, *fakes* the SSO
step by injecting a fixed `Remote-User`, and routes the three planes.

```yaml title="docker-compose.yml"
services:
  gotifacts:
    image: ghcr.io/lmgarret/gotifacts:edge
    env_file: .env
    expose:
      - "8080"
    volumes:
      - data:/data

  proxy:
    image: caddy:2
    ports:
      - "80:80"
    configs:
      - source: caddy
        target: /etc/caddy/Caddyfile
    depends_on:
      - gotifacts

configs:
  caddy:
    content: |
      {
        auto_https off
      }

      # Apex: portal + management + ingest.
      http://gotifacts.localhost {
        # Strip any client-supplied identity header, then inject a fake user.
        request_header -Remote-User
        handle /ingest/* {
          reverse_proxy gotifacts:8080
        }
        handle {
          request_header Remote-User you@example.com
          reverse_proxy gotifacts:8080
        }
      }

      # Site content for one and two label levels.
      http://*.gotifacts.localhost,
      http://*.*.gotifacts.localhost {
        request_header -Remote-User
        reverse_proxy gotifacts:8080
        header Content-Security-Policy "frame-ancestors http://gotifacts.localhost"
      }

volumes:
  data:
```

:::caution
Injecting a fixed `Remote-User` like this is **only** safe locally. In
production your proxy must authenticate the user with real SSO and strip any
client-supplied copy of the header. See the
[auth model](/gotifacts/explanation/auth-model/).
:::

## Step 4 — Start it

```sh
docker compose up -d
```

Most browsers resolve `*.localhost` to `127.0.0.1` automatically. If yours does
not, add `gotifacts.localhost` to your hosts file.

## Step 5 — Log in to the portal

Open **http://gotifacts.localhost**. Because Caddy injects your identity, you're
logged straight in as the admin user from `GOTIFACTS_ADMIN_USERS`. You'll see an
empty portal — no sites yet.

To confirm the API sees you:

```sh
curl -s http://gotifacts.localhost/api/me
# {"user":"you@example.com","is_admin":true,"base_domain":"gotifacts.localhost",...}
```

## What you built

```
http://gotifacts.localhost            → portal + /api + /ingest
http://<slug>.<group>.gotifacts.localhost → your published sites
```

You have a running instance with an admin session. Next, publish something into
it.

## Next steps

- [Publish your first site](/gotifacts/tutorials/publish-first-site/)
- Tear down with `docker compose down -v` when you're done.
