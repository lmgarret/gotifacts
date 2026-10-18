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
| `rollback` | list revisions and restore a previous version, or promote a chosen revision — `GET`/`POST /ingest/sites/…/revisions[/rollback]` |
| `patch` | edit site metadata — `PATCH /ingest/sites/…` |
| `purge` | permanently destroy a quarantined site — `POST /ingest/sites/…/purge` |
| `read` | read site content over MCP — list sites and files, read text files, search. **MCP only**: no ingest endpoint uses it |

Unpublish and purge are intentionally separate: `unpublish` takes a site offline (recoverable within the TTL), while `purge` is irreversible. Automation that only tears down previews should hold `unpublish`; automation that also needs to destroy data permanently should hold both.

Over MCP, `purge_site` additionally asks the user to confirm before deleting anything, when the connected client supports elicitation — see [Confirming a purge](/gotifacts/guides/connect-claude-mcp/#confirming-a-purge). That is a prompt, not a permission: the grant is still what decides whether the call is allowed at all, and a client that cannot be prompted purges in one call.

### Reading over MCP

`read` is checked per site, like every other capability, and write capabilities
do **not** imply it: a connection that can `publish` to `claude` cannot read
`claude` back unless it also holds `read` there. Sites a grant does not cover
are reported as not found or not readable, with the same error whether the site
is missing, deleted or just out of scope.

A **hidden** site is only hidden from the portal listing for non-admins; it is
still served at its URL. Accordingly, a `read` grant that covers a hidden site
can read it. Site listings leave hidden sites out by default and include them
only when asked.

An [MCP connection](/gotifacts/guides/connect-claude-mcp/) can carry several
grants, so one connector can publish to one place (for example `claude`) and read
another (for example `docs`).

## Roles

| Role | Granted by | Can do |
| --- | --- | --- |
| **Admin** | forward-auth allowlist **or** an `admin` key | everything: manage keys + all capabilities on every site |
| **Scoped key** | a key with one or more grants | only the granted capabilities, confined to each grant's target |
| **Viewer** | any authenticated forward-auth user | view the portal, `GET /api/sites`, and browse/download any site's files and revisions (`GET /api/sites/…/revisions…`) — hidden sites stay admin-only |

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
grant model, edited with the same grant editor as the API Keys view: at the consent
screen the approver builds one or more grants, each with a scope (group subtree,
single site, or all sites) and the capabilities to allow. The first grant is
prefilled with `GOTIFACTS_MCP_GROUP` + `publish`. `read` is a regular capability on
that screen, ticked per grant like the others, so one connector can for example
publish to `claude` and read `docs`. A global (all sites) grant needs the same
risk acknowledgement as an API key. The connector can never act outside what was granted.
