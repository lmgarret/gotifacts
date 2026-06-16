---
title: Enable versioning & roll back
description: Keep previous site versions on replace and restore them when a deploy goes wrong.
sidebar:
  order: 8
---

By default, publishing replaces a site in place with no history. Enable
**versioning** to retain previous versions on each replace and unlock rollback.

## Enable it

Set these and restart (see the [configuration reference](/gotifacts/reference/configuration/)):

```ini
GOTIFACTS_VERSIONING_ENABLED=true
GOTIFACTS_VERSIONING_KEEP=5   # versions retained per site
```

With versioning on, each `POST /ingest/sites` that replaces an existing site
archives the previous version first, keeping up to `KEEP` of them.

## Roll back

Rollback restores the **latest archived version** of a site.

From CI or a script (ingest plane — requires the `rollback` capability):

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>/rollback"
```

An admin can also roll back from the portal, or via the management API
(`POST /api/sites/{group}/{slug}/rollback`).

:::note
Rollback requires versioning to have been enabled when the previous version was
published — there's nothing to restore otherwise.
:::

## Tips

- Give automation a dedicated key with `rollback` (and usually `publish`) on the
  relevant group; see [create & scope API keys](/gotifacts/guides/create-api-keys/).
- `KEEP` bounds disk usage: each retained version is a full copy of the site's
  files under `/data`.
