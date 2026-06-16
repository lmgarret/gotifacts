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
