---
title: Permissions & capabilities
description: The capability-based grant model for API keys and MCP tokens.
sidebar:
  order: 3
---

Access on the ingest plane is **capability-based**. A key (or MCP token) is
either an **admin** superuser or a set of **grants**, each binding a set of
capabilities to a target.

## Capabilities

| Capability | Allows |
| --- | --- |
| `publish` | create/replace sites — `POST /ingest/sites` |
| `unpublish` | soft-delete sites — `DELETE /ingest/sites/…` (files kept in quarantine) |
| `rollback` | restore a site's previous version — `POST /ingest/sites/…/rollback` |
| `patch` | edit site metadata — `PATCH /ingest/sites/…` |
| `purge` | permanently destroy a quarantined site — `POST /ingest/sites/…/purge` |

Unpublish and purge are intentionally separate: `unpublish` takes a site offline (recoverable within the TTL), while `purge` is irreversible. Automation that only tears down previews should hold `unpublish`; automation that also needs to destroy data permanently should hold both.

## Roles

| Role | Granted by | Can do |
| --- | --- | --- |
| **Admin** | forward-auth allowlist **or** an `admin` key | everything: manage keys + all capabilities on every site |
| **Scoped key** | a key with one or more grants | only the granted capabilities, confined to each grant's target |
| **Viewer** | any authenticated forward-auth user | view the portal and `GET /api/sites` |

## Targets

A grant's target is either a **group** or a single **site**:

- A **group** grant on `docs` owns the `docs` subdomain and everything beneath
  it: `docs.<base>` itself (the flat site group `""`, slug `docs`) **and** every
  site under `*.docs.<base>` (e.g. `app.docs.<base>` = group `docs`, slug `app`).
- A **site** grant on `docs/app` is confined to exactly that one site
  (`app.docs.<base>`) — not its children, not its siblings.
- A **group** grant with an **empty** target means *all sites* (global).

Targets are free-text (each label must match
`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`) — you don't pre-register them.

## API key properties

- Token format `gtf_<base64url-32B>`, shown in plaintext **once** at creation.
- Only the **SHA-256 hash** is stored; lookups are constant-time.
- A key may optionally **expire** (an instant, or never — the default); past it,
  the key is rejected like an unknown token.

Mint keys in the portal (**API Keys** view, admin only) or via the
[CLI](/gotifacts/reference/cli/). There is **no bootstrap key**: set
`GOTIFACTS_ADMIN_USERS`, log in through your proxy, and create keys in the UI.

:::note[Backward compatibility]
Existing tokens keep working unchanged. A migration backfills grants from the old
`scope`/`group_restriction` model — old `publish` keys get an equivalent
`publish` grant; old `admin` keys become admin superusers.
:::

## MCP tokens

[MCP connector](/gotifacts/guides/connect-claude-mcp/) tokens carry the **same**
grant model: at the consent screen the approver picks a target (group subtree or
single site) and the capabilities to allow, prefilled from `GOTIFACTS_MCP_GROUP`
+ `publish`. The connector can never act outside what was granted.
