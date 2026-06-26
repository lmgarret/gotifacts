---
title: Publish from CI
description: Deploy build artifacts or PR previews to gotifacts from a CI pipeline.
sidebar:
  order: 5
---

CI is the canonical machine publisher. Give the pipeline a scoped API key and
`POST` your build to the ingest API. Nothing here touches server or proxy
credentials.

## 1. Mint a scoped key

Create a key limited to the group you deploy into. For PR previews that get
cleaned up, include `unpublish`:

```sh
gotifacts keys create --name ci --grant "previews:publish,unpublish"
```

Store the printed token as a CI secret, e.g. `GOTIFACTS_API_KEY`. See
[create & scope API keys](/gotifacts/guides/create-api-keys/).

## 2. Publish a bundle

For a multi-file site, upload a `.tar.gz` with a top-level `index.html` as the
`bundle` part:

```sh
tar -czf site.tgz -C ./dist .
printf '{"group":"previews","slug":"pr-%s","title":"PR #%s"}' "$PR" "$PR" > meta.json

curl -fsS \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  -F 'meta=<meta.json;type=application/json' \
  -F 'bundle=@site.tgz;type=application/gzip' \
  "${GOTIFACTS_URL}/ingest/sites"
```

For a single self-contained page, use the `index` part instead of `bundle` (see
[publishing your first site](/gotifacts/tutorials/publish-first-site/)).

Publishing is idempotent — re-running replaces the site at the same
`group`/`slug`.

A group and a flat site may share a name: publishing previews under
`group: "decks"` (reachable at `pr-6.decks.<base>`) coexists with a flat `decks`
site at `decks.<base>`. Each site's content is isolated, so deploying the flat
site never disturbs the previews beneath it. See the
[URL ⇄ path convention](/gotifacts/reference/url-path-convention/).

## 3. Tear down a preview

When a PR closes, delete its preview (requires the `unpublish` capability):

```sh
curl -fsS -X DELETE \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  "${GOTIFACTS_URL}/ingest/sites/previews/pr-${PR}"
```

## Example: GitHub Actions

```yaml title=".github/workflows/preview.yml"
- name: Publish preview
  env:
    GOTIFACTS_URL: https://example.com
    GOTIFACTS_API_KEY: ${{ secrets.GOTIFACTS_API_KEY }}
    PR: ${{ github.event.number }}
  run: |
    tar -czf site.tgz -C ./dist .
    printf '{"group":"previews","slug":"pr-%s","title":"PR #%s"}' "$PR" "$PR" > meta.json
    curl -fsS \
      -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
      -F 'meta=<meta.json;type=application/json' \
      -F 'bundle=@site.tgz;type=application/gzip' \
      "${GOTIFACTS_URL}/ingest/sites"
```

## Troubleshooting

| Status | Cause |
| --- | --- |
| `401` | Missing or invalid API key. |
| `403` | The key lacks the capability for that group/site. |
| `400` | Invalid path (bad label or too deep), or missing `index.html`. |

See the full [HTTP API reference](/gotifacts/reference/api/).
