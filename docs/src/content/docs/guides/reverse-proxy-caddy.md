---
title: Configure Caddy
description: Front gotifacts with Caddy for automatic TLS, forward-auth, and host-based routing.
sidebar:
  order: 3
---

Caddy is a convenient front end for gotifacts: it obtains TLS certificates
automatically and has first-class `forward_auth` support. This page shows the
routing split; adapt the SSO backend to your provider.

:::danger
gotifacts trusts the identity header **only** from `GOTIFACTS_TRUSTED_PROXIES`.
Always strip any client-supplied copy before injecting the authenticated one,
and make sure Caddy's IP is in that list. See the
[auth model](/gotifacts/explanation/auth-model/).
:::

## Reference config

A complete, commented example lives in the repo at
[`examples/caddy/Caddyfile`](https://github.com/lmgarret/gotifacts/blob/main/examples/caddy/Caddyfile).
The essentials:

```text
# SECURITY: never trust a client-supplied identity header.
(strip_identity) {
    request_header -Remote-User
}

# Apex: portal + management + ingest.
example.com {
    import strip_identity

    # Ingest plane: forward-auth OFF (API key enforced by gotifacts).
    handle /ingest/* {
        request_body {
            max_size 64MB
        }
        reverse_proxy gotifacts:8080
    }

    # Management plane: forward-auth ON.
    handle {
        forward_auth your-sso-backend:9091 {
            uri /api/verify?rd=https://auth.example.com
            copy_headers Remote-User      # inject the authenticated user
        }
        reverse_proxy gotifacts:8080
    }
}
```

## Site content

Caddy wildcards match a single label, so one and two label levels need separate
blocks. Both strip the identity header and add the framing policy for
[portal thumbnails](/gotifacts/guides/portal-thumbnails/):

```text
*.example.com,
*.*.example.com {
    import strip_identity
    reverse_proxy gotifacts:8080
    header Content-Security-Policy "frame-ancestors https://example.com"
}
```

## MCP endpoints

If you enable the [MCP connector](/gotifacts/guides/connect-claude-mcp/), add a
`handle` that bypasses forward-auth for the machine endpoints, while
`/mcp/oauth/authorize` falls through to the authenticated catch-all. A `handle`
takes at most one matcher token, so list the paths in a named matcher rather
than inline:

```text
@mcp_machine path /mcp /mcp/oauth/token /mcp/oauth/register /.well-known/oauth-*
handle @mcp_machine {
    reverse_proxy gotifacts:8080
}
```
