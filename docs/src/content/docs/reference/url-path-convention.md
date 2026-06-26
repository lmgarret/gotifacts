---
title: URL ⇄ path convention
description: How a request host maps to a site directory, and the rules that constrain it.
sidebar:
  order: 3
---

gotifacts routes purely by the request `Host`. The apex host (`==
GOTIFACTS_BASE_DOMAIN`) serves the portal and APIs; every other host maps to a
site directory.

## The mapping

Strip `base_domain` from a host. The remaining sub-labels, read left→right, run
`[most-specific … least-specific]`. The site's **logical path** is those labels
**reversed**, and its files live under a reserved `@site` leaf within it:

| Host | Served directory | `group` | `slug` |
| --- | --- | --- | --- |
| `app.claude.<base>` | `sites/claude/app/@site` | `claude` | `app` |
| `a.sub.grp.<base>` | `sites/grp/sub/a/@site` | `grp/sub` | `a` |
| `demo.<base>` | `sites/demo/@site` | *(flat)* | `demo` |

## The `@site` leaf

Each site's content is stored in a reserved `@site` subdirectory under its
logical path, rather than directly in it. The `@` can never appear in a label
(labels must match the pattern below), so `@site` never collides with a slug or
group segment. This lets the same path be **both** a site and a group parent:

- `decks.<base>` → `sites/decks/@site` — a flat site named `decks`.
- `pr-6.decks.<base>` → `sites/decks/pr-6/@site` — a site in the `decks` group.

Both coexist: publishing, versioning, or unpublishing one only ever touches its
own `@site` leaf, so a deploy of the flat `decks` site never disturbs the
`pr-N` previews beneath it. Because content lives in `@site`, a path *on* the
flat host (e.g. `decks.<base>/pr-6`) does **not** expose the `pr-6` member —
members are reachable only via their own host.

Upgrading an existing deployment to this layout is a one-time migration; see
[`gotifacts migrate-layout`](/gotifacts/reference/cli/).

## Rules

- **Total depth (group segments + slug) ≤ 3.** Deeper hosts are rejected.
- Each label must match `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`.
- A site is identified on publish by `group` (0–2 segments, may be empty) +
  `slug` (the leaf).

## Identifying a site in the API

Path parameters in the [HTTP API](/gotifacts/reference/api/) carry the full site
path — the group segments followed by the slug — for example
`/ingest/sites/claude/app` or `/ingest/sites/demo` for a flat site.
