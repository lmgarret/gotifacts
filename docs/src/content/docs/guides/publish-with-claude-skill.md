---
title: Publish with the Claude skill
description: Use the distributable Claude skill to publish a page wherever you control the environment.
sidebar:
  order: 6
---

gotifacts ships a distributable **Claude skill** that asks for consent, writes a
self-contained `index.html`, picks a URL-safe `slug`/`group`, publishes via the
single-`index` ingest form, and reports the URL. It only ever handles the API
key + URL — never server or proxy credentials.

The skill lives in the repo at
[`examples/skill/SKILL.md`](https://github.com/lmgarret/gotifacts/blob/main/examples/skill/SKILL.md).

## Where it works

The skill needs `GOTIFACTS_URL` and `GOTIFACTS_API_KEY` in the environment, so it
works wherever **you** control that environment:

- Claude Code
- CI
- the Claude API code-execution tool

It does **not** work in default claude.ai / Claude mobile conversations, which
can't inject environment variables. For those, use the
[MCP connector](/gotifacts/guides/connect-claude-mcp/) instead.

## Install

```sh
mkdir -p ~/.claude/skills/publish-to-gotifacts
cp examples/skill/SKILL.md ~/.claude/skills/publish-to-gotifacts/
```

## Configure

Set the two variables before use:

```sh
export GOTIFACTS_URL=https://example.com
export GOTIFACTS_API_KEY=gtf_…   # a publish-scoped key, ideally limited to the claude group
```

Mint that key as in [create & scope API keys](/gotifacts/guides/create-api-keys/),
e.g. `--grant "claude:publish"`.

## Use

Ask Claude to "publish this as a web page" (or similar). The skill will:

1. Summarize what it's about to publish and ask you to confirm.
2. Produce a self-contained `index.html`.
3. Pick a `slug` and `group` (default `claude`) within your key's scope.
4. `POST` to `${GOTIFACTS_URL}/ingest/sites` and report the resulting URL.

Re-publishing the same `group`/`slug` replaces the existing site.
