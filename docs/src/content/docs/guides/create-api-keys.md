---
title: Create & scope API keys
description: Mint capability-scoped API keys for CI and bots, in the portal or via the CLI.
sidebar:
  order: 4
---

The ingest plane is authorized by **capability-scoped API keys**. A key is
either an **admin** superuser or a set of **grants**, each binding capabilities
(`publish`, `unpublish`, `rollback`, `patch`) to a target. For the full model,
see the [permissions reference](/gotifacts/reference/permissions/).

There is **no bootstrap key**: set `GOTIFACTS_ADMIN_USERS`, log in through your
proxy, and create keys in the UI. The CLI is the headless fallback.

## In the portal

Open the **API Keys** view (admin only), choose a name, pick a target (a group
subtree or a single site) and the capabilities to allow, optionally set an
expiry, and create. The plaintext token is shown **once** — copy it immediately.

## With the CLI

Run inside the container (`docker compose exec gotifacts …`):

```sh
# A CI key that can deploy AND tear down PR previews — no admin rights:
gotifacts keys create --name ci --grant "previews:publish,unpublish"

# A key confined to a single site:
gotifacts keys create --name docs-bot --grant-site "docs/app:publish,patch"

# An expiring key (also: --expires-at 2026-12-31):
gotifacts keys create --name temp --grant "docs:publish" --expires-in 720h

# Multiple grants, a global grant, and an admin key:
gotifacts keys create --name release --grant "claude:publish" --grant ":unpublish"
gotifacts keys create --name root --admin

gotifacts keys list
gotifacts keys revoke --id 3
```

- `--grant "group:caps"` targets a **group subtree** (empty group = all sites).
- `--grant-site "group/slug:caps"` targets **one exact site**.

See the [CLI reference](/gotifacts/reference/cli/) for every flag.

## Properties to know

- Tokens have the format `gtf_<base64url-32B>` and are shown in plaintext **only
  once**, at creation.
- Only the **SHA-256 hash** is stored; lookups are constant-time; tokens are
  never logged.
- Keys may optionally **expire**; past the expiry they're rejected like an
  unknown token.

:::tip
Grant the narrowest target and capability set that does the job. A CI deploy
usually needs only `publish` (plus `unpublish` if it tears down previews) on a
single group.
:::
