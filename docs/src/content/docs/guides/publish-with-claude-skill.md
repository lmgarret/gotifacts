---
title: Publish with the Claude skill
description: Use the distributable Claude skill to publish a page wherever you control the environment.
sidebar:
  order: 6
---

gotifacts ships a distributable **Claude skill** (`gotifacts`) that covers the
full site lifecycle: publish, unpublish, update metadata, rollback, restore, and purge. The
**golden path** is the [MCP connector](/gotifacts/guides/connect-claude-mcp/) —
no env vars needed, Claude authenticates via OAuth. For Claude Code, CI, or any
environment without a connector, it falls back to `GOTIFACTS_URL` + `GOTIFACTS_API_KEY`.

The skill lives in the repo at
[`examples/skill/SKILL.md`](https://github.com/lmgarret/gotifacts/blob/main/examples/skill/SKILL.md).

## Where it works

| Environment | Path |
| --- | --- |
| claude.ai / Claude mobile | MCP connector (OAuth — no env vars) |
| Claude Code / Claude API | MCP connector **or** API key + env vars |
| CI pipelines | API key + env vars |

## Install

```sh
mkdir -p ~/.claude/skills/gotifacts
cp examples/skill/SKILL.md ~/.claude/skills/gotifacts/
```

## Configure (API key path)

Set the two variables before use when not using the MCP connector:

```sh
export GOTIFACTS_URL=https://example.com
export GOTIFACTS_API_KEY=gtf_…   # scoped key for the claude group
```

Mint that key as in [create & scope API keys](/gotifacts/guides/create-api-keys/),
e.g. `--grant "claude:publish,unpublish,patch,rollback,purge"`.

## Use

Ask Claude to "publish this as a web page", "take that down", "update the
description", or "roll back to the previous version". The skill will ask for
consent, then call the appropriate MCP tool (or `curl` command). Operations:

| Ask Claude to... | Skill action |
| --- | --- |
| Publish / share a page | `publish_site` / POST |
| Unpublish / take down | `unpublish_site` / DELETE |
| Update title or tags | `update_site` / PATCH |
| Roll back to previous | `rollback_site` / POST rollback |
| Restore from quarantine | `restore_site` / POST restore |
| Permanently delete | `purge_site` / POST purge |

Re-publishing the same `group`/`slug` replaces the existing site. Unpublishing
keeps files in quarantine for the server's configured TTL (default 30 days)
before permanent removal. `restore_site` brings the site back online within the
grace period; `purge_site` destroys it immediately and is irreversible.
