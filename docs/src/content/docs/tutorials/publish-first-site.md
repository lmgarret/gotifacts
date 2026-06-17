---
title: Publish your first site
description: Mint an API key, publish a single HTML page, and view it on its own subdomain.
sidebar:
  order: 2
---

This tutorial picks up where [Run gotifacts locally](/gotifacts/tutorials/run-locally/)
left off: you have a running instance and you're logged in to the portal as an
admin. Now you'll publish a page and watch it appear on its own subdomain.

## Step 1 — Mint a publish key

Publishing happens on the **ingest plane**, which is authenticated by an API key
(never by your browser session). Mint one with the CLI inside the running
container:

```sh
docker compose exec gotifacts \
  gotifacts keys create --name tutorial --grant "claude:publish"
```

The command prints the token **once** — copy it. It looks like `gtf_…`. Save it:

```sh
export GOTIFACTS_API_KEY=gtf_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

The grant `claude:publish` means: *may publish to the `claude` group subtree, and
nothing else*. More on [permissions](/gotifacts/reference/permissions/).

## Step 2 — Write a page

Create a self-contained HTML file:

```html title="index.html"
<!doctype html>
<html>
  <head><meta charset="utf-8" /><title>Hello gotifacts</title></head>
  <body style="font-family: system-ui; padding: 4rem; text-align: center">
    <h1>👋 My first gotifacts site</h1>
    <p>Published over the ingest API.</p>
  </body>
</html>
```

## Step 3 — Describe it

The publish request carries a small JSON `meta` part identifying the site:

```sh
printf '{"group":"claude","slug":"hello","title":"Hello gotifacts"}' > meta.json
```

`group` + `slug` decide the URL. Here the site will live at
`hello.claude.gotifacts.localhost` — see the
[URL ⇄ path convention](/gotifacts/reference/url-path-convention/).

## Step 4 — Publish

Send both parts to the ingest endpoint:

```sh
curl -fsS \
  -H "Authorization: Bearer ${GOTIFACTS_API_KEY}" \
  -F 'meta=<meta.json;type=application/json' \
  -F 'index=@index.html;type=text/html' \
  http://gotifacts.localhost/ingest/sites
```

You get back JSON with the public URL:

```json
{ "url": "http://hello.claude.gotifacts.localhost", "group": "claude", "slug": "hello", "updated_at": "..." }
```

## Step 5 — View it

Open **http://hello.claude.gotifacts.localhost** — there's your page. Refresh the
**portal** at http://gotifacts.localhost and you'll see the site listed, with a
live thumbnail.

## Step 6 — Republish

Publishing is idempotent: the same `group`/`slug` replaces the site. Edit
`index.html`, run the Step 4 command again, and the URL updates in place.

## What you learned

- The ingest plane is key-authenticated and separate from the portal.
- A grant confines a key to a group subtree.
- `group` + `slug` deterministically map to a subdomain.

## Next steps

- [Create & scope API keys](/gotifacts/guides/create-api-keys/) for CI and bots
- [Publish from CI](/gotifacts/guides/publish-from-ci/)
- [Connect Claude via the MCP connector](/gotifacts/guides/connect-claude-mcp/)
