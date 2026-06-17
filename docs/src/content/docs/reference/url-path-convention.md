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
`[most-specific … least-specific]`. The served directory is those labels
**reversed**:

| Host | Served directory | `group` | `slug` |
| --- | --- | --- | --- |
| `app.claude.<base>` | `sites/claude/app` | `claude` | `app` |
| `a.sub.grp.<base>` | `sites/grp/sub/a` | `grp/sub` | `a` |
| `demo.<base>` | `sites/demo` | *(flat)* | `demo` |

## Rules

- **Total depth (group segments + slug) ≤ 3.** Deeper hosts are rejected.
- Each label must match `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`.
- A site is identified on publish by `group` (0–2 segments, may be empty) +
  `slug` (the leaf).

## Identifying a site in the API

Path parameters in the [HTTP API](/gotifacts/reference/api/) carry the full site
path — the group segments followed by the slug — for example
`/ingest/sites/claude/app` or `/ingest/sites/demo` for a flat site.
