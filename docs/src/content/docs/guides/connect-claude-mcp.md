---
title: Connect Claude via MCP
description: Enable the OAuth-protected MCP connector so claude.ai mobile/web can publish.
sidebar:
  order: 7
---

For the consumer Claude apps (claude.ai mobile/web), the
[skill](/gotifacts/guides/publish-with-claude-skill/) can't inject environment
variables. Instead, expose gotifacts' **MCP connector**: an OAuth 2.1-protected
[Model Context Protocol](https://modelcontextprotocol.io) server at `/mcp` with a
single `publish_site` tool. Claude's "custom connector" UI authenticates remote
MCP servers exclusively via OAuth, so this is the only path that works on
mobile/web.

## 1. Enable it on the server

Set in your environment (see the [configuration reference](/gotifacts/reference/configuration/)):

```ini
GOTIFACTS_MCP_ENABLED=true
# Who may grant connector consent. Keep this tight — it decides who can publish.
GOTIFACTS_MCP_ALLOWED_USERS=you@example.com
```

It requires `GOTIFACTS_TRUSTED_PROXIES` (the consent step is forward-auth'd) and
falls back to `GOTIFACTS_ADMIN_USERS` if no allowlist is set.

## 2. Route it in your proxy

The MCP endpoints split across the two planes:

| Endpoint | Forward-auth | Why |
| --- | --- | --- |
| `/mcp/oauth/authorize` | **ON** | browser consent, authenticated by your SSO |
| `/mcp`, `/mcp/oauth/token`, `/mcp/oauth/register`, `/.well-known/oauth-*` | **OFF** | called server-to-server by Claude (OAuth-guarded) |

The [nginx](/gotifacts/guides/reverse-proxy-nginx/) and
[Caddy](/gotifacts/guides/reverse-proxy-caddy/) guides show the exact patterns.

## 3. Connect from Claude

In Claude → **Settings → Connectors → Add custom connector**, enter
`https://<your-base-domain>/mcp`. Complete the SSO consent, where you pick:

- a **target** — a group subtree or a single site, prefilled from
  `GOTIFACTS_MCP_GROUP` (default `claude`), and
- the **capabilities** to allow (`publish`, `patch`, `unpublish`, `rollback`).

The connector can never act outside what you granted. Then ask Claude to publish
a page.

For the **API MCP connector** or **Claude Code**, the same server works with a
token obtained through the OAuth flow.

## Manage connections

Each consent creates a *connection* you can review and revoke. Admins see them
in the portal's **Connections** view, via `GET /api/mcp/connections`, or
headless:

```sh
gotifacts mcp connections        # list active connections
gotifacts mcp revoke --id <id>   # revoke one — access is lost immediately
```

## How it fits the model

Tokens carry the same **grant model** as [API keys](/gotifacts/reference/permissions/):
the consent screen is the gate that decides *who* may publish, and the chosen
target + capabilities decide *what*. Dynamic Client Registration (RFC 7591) lets
users add the connector by pasting the URL; it only issues a `client_id` and
grants no access on its own. For the deeper rationale see the
[auth model](/gotifacts/explanation/auth-model/).
