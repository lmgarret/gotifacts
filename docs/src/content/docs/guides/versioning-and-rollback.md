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

## Browse files & revisions

In the portal, open a site and switch to the **Files** tab to browse its
deployed files. Pick a revision from the selector — the live **Current**
version plus any retained archived versions — to inspect that snapshot's file
tree. You can download an individual file, or the whole revision as a `.zip`.

Browsing and downloading are available to any signed-in user; rolling back is
admin-only.

## Roll back

Rollback promotes a previous version to live. The current live content is
archived first, so the action is itself reversible.

From the portal, an admin picks a revision in the **Files** tab and chooses
**Roll back to this revision**.

From CI or a script (ingest plane — requires the `rollback` capability):

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>/rollback"
```

With no body, rollback restores the **latest archived version**. To target a
specific revision, pass its id (from the revision list) in the body:

```sh
curl -fsS -X POST \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"revision":"20060102T150405.000000000Z"}' \
  "${GOTIFACTS_URL}/ingest/sites/<group>/<slug>/rollback"
```

The same applies to the management API
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
