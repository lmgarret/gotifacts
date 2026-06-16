---
title: Configure nginx
description: Front gotifacts with nginx for TLS, forward-auth, and host-based routing.
sidebar:
  order: 2
---

gotifacts is proxy-agnostic and serves plain HTTP. nginx (or any proxy)
provides TLS, runs forward-auth/SSO, and routes the planes. This page shows the
routing split; adapt the SSO subrequest to your provider (Authelia,
oauth2-proxy, Keycloak gatekeeper, …).

:::danger
gotifacts trusts the identity header **only** from `GOTIFACTS_TRUSTED_PROXIES`.
Always strip any client-supplied copy before injecting the authenticated one,
and make sure nginx's IP is in that list. See the
[auth model](/gotifacts/explanation/auth-model/).
:::

## The routing split

| Location | Forward-auth | Serves |
| --- | --- | --- |
| apex `/` and `/api/*` | **ON** (inject identity header) | portal + management API |
| apex `/ingest/*` | **OFF** (API key) | machine publish API |
| `/mcp/oauth/authorize` | **ON** (browser consent) | MCP consent screen |
| `/mcp`, `/mcp/oauth/token`, `/mcp/oauth/register`, `/.well-known/oauth-*` | **OFF** (OAuth) | MCP machine endpoints |
| `*.base`, `*.*.base` | **OFF** | static site content |

## Reference config

A complete, commented example lives in the repo at
[`examples/nginx/gotifacts.conf`](https://github.com/lmgarret/gotifacts/blob/main/examples/nginx/gotifacts.conf).
The essentials:

```nginx
# SECURITY: strip any client-supplied identity header on the way in.
proxy_set_header Remote-User "";

# Ingest plane: forward-auth OFF (API key enforced by gotifacts).
location /ingest/ {
    client_max_body_size 64m;
    proxy_pass http://gotifacts;
    proxy_set_header Host $host;
    proxy_set_header Remote-User "";
}

# Management plane: forward-auth ON.
location / {
    auth_request /auth;
    auth_request_set $authed_user $upstream_http_remote_user;
    proxy_pass http://gotifacts;
    proxy_set_header Host $host;
    proxy_set_header Remote-User $authed_user;   # inject the real user
}
```

Site content is served from a separate `server` block matching `*.base` (and
`*.*.base` for deeper hosts), which also adds the framing header so the portal
can render [live thumbnails](/gotifacts/guides/portal-thumbnails/):

```nginx
add_header Content-Security-Policy "frame-ancestors https://example.com" always;
```

## MCP endpoints

If you enable the [MCP connector](/gotifacts/guides/connect-claude-mcp/), the
machine endpoints must bypass forward-auth while `/mcp/oauth/authorize` stays
behind it. The reference config uses a regex `location` to match exactly the
machine endpoints; see the file for the precise pattern and the long-timeout /
buffering settings needed for streamable HTTP.
