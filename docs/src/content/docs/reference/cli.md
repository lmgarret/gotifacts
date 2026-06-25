---
title: CLI reference
description: The gotifacts command-line interface — serve, keys, and mcp.
sidebar:
  order: 2
---

The `gotifacts` binary is both the server and a small management CLI. When no
command is given, it defaults to `serve`. Run subcommands in your deployment with
`docker compose exec gotifacts gotifacts <command>`.

```
gotifacts — static site host + portal

Commands:
  serve                                   Run the HTTP server (default)
  keys create …                           Create an API key (prints token once)
  keys list                               List API keys
  keys revoke --id ID                     Delete an API key
  mcp connections                         List active MCP connections
  mcp revoke --id ID                      Revoke an MCP connection
  migrate-layout [--dry-run]              Relocate site content into @site leaves
  version                                 Print the version
  help                                    Show usage
```

## `serve`

Runs the HTTP server on `GOTIFACTS_LISTEN_ADDR`. Takes no flags — all
configuration comes from [environment variables](/gotifacts/reference/configuration/).

```sh
gotifacts serve
```

## `keys`

Manage ingest-plane API keys. See [permissions](/gotifacts/reference/permissions/)
for the grant model and [create & scope API keys](/gotifacts/guides/create-api-keys/)
for examples.

### `keys create`

Creates a key and prints its plaintext token **once**.

| Flag | Description |
| --- | --- |
| `--name STRING` | **Required.** Human-readable key name. |
| `--admin` | Make the key an admin superuser (ignores grants). |
| `--grant "GROUP:CAPS"` | Add a **group-subtree** grant. Repeatable. Empty group = all sites. |
| `--grant-site "GROUP/SLUG:CAPS"` | Add a **single-site** grant. Repeatable. |
| `--expires-in DURATION` | Relative expiry, Go duration syntax (e.g. `720h`). |
| `--expires-at WHEN` | Absolute expiry, RFC3339 or `YYYY-MM-DD`. Overrides `--expires-in`. |

`CAPS` is a comma-separated list of capabilities: `publish`, `unpublish`,
`rollback`, `patch`.

```sh
gotifacts keys create --name ci --grant "previews:publish,unpublish"
gotifacts keys create --name docs-bot --grant-site "docs/app:publish,patch"
gotifacts keys create --name root --admin
```

### `keys list`

Lists all API keys (never their plaintext tokens). No flags.

### `keys revoke`

| Flag | Description |
| --- | --- |
| `--id INT` | **Required.** ID of the key to delete. |

```sh
gotifacts keys revoke --id 3
```

## `mcp`

Manage [MCP connector](/gotifacts/guides/connect-claude-mcp/) connections.

### `mcp connections`

Lists active MCP connections (client, user, scope, last used). Also accepts the
alias `mcp list`. No flags.

### `mcp revoke`

| Flag | Description |
| --- | --- |
| `--id STRING` | **Required.** ID of the connection to revoke. |

```sh
gotifacts mcp revoke --id <id>
```

## `migrate-layout`

Relocates each site's published files into its reserved `@site` leaf (see the
[URL ⇄ path convention](/gotifacts/reference/url-path-convention/)). Run it once
when upgrading a deployment that was created before the `@site` layout existed;
without it, previously published sites would 404. The command is **idempotent**
— already-migrated sites are skipped, so it is safe to re-run.

| Flag | Description |
| --- | --- |
| `--dry-run`, `-n` | Report what would move without changing anything. |

It reads the registry to tell a site's own content apart from nested child-site
directories (which are preserved in place). Because it walks the data volume,
run it during a brief maintenance window so it doesn't race live publishes:

```sh
docker compose stop gotifacts
docker compose run --rm gotifacts migrate-layout
docker compose up -d gotifacts
```

Any content that isn't backed by a registry row (for example a stray
sub-directory that was accidentally nested inside another site) is folded into
that site's `@site` as its own content; re-publish it as a proper site
afterwards if it should be its own entry.
