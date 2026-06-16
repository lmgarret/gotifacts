---
title: Deploy a site from the portal
description: Drag-and-drop an HTML file or a zip/tar.gz archive in the portal to publish a static site — no API key required.
sidebar:
  order: 10
---

For quick, one-off deploys you don't need an API key or the CLI: an **admin** can
publish a site straight from the portal by dropping a file into a modal.

:::note
This is an **admin-only** convenience. It runs on the management plane (your
forward-auth identity), not the ingest plane — so it needs no API key. Automated
and CI publishing should still use [scoped API keys](/gotifacts/guides/create-api-keys/).
:::

## Steps

1. Open the portal and make sure you're logged in as an admin.
2. Click **+ Add site** in the toolbar.
3. Drag a file onto the drop zone (or click to choose one). Accepted inputs:
   - a single **`.html`** file (served as `index.html` — it must be
     self-contained, i.e. inline its CSS/JS), or
   - a **`.zip`** or **`.tar.gz`** archive containing an `index.html`.
4. Fill in the **slug** (the subdomain) and, optionally, a **group**, title,
   description, and tags. The modal previews the resulting host, e.g.
   `my-site.previews.example.com`.
5. Click **Deploy**. The site goes live immediately at the shown URL.

## How archives are handled

The archive format is detected from the file's magic bytes, not its name. For a
**zip**, if every file lives under one common top-level directory — as happens
when you zip a folder (`site/index.html`, `site/assets/…`) — that wrapper
directory is stripped so `index.html` ends up at the site root.

The same safety limits as the ingest API apply: the upload, total extracted
size, and entry count are all capped (see
[configuration](/gotifacts/reference/configuration/)), and archive entries that
escape the target directory or are symlinks are rejected.

## Things to know

- **Overwrites are silent.** Deploying to an existing group/slug replaces the
  live site. The modal warns you when the target already exists. With
  [versioning enabled](/gotifacts/guides/versioning-and-rollback/) the previous
  version is retained and can be rolled back.
- **Wildcard DNS/TLS is a prerequisite.** A freshly deployed site only resolves
  if your reverse proxy already serves `*.<base>`.
- **You're hosting arbitrary HTML/JS.** Each site is its own origin (subdomain),
  which is the isolation boundary — keep deploys to content you trust.
