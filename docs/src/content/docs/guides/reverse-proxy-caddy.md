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

## On-demand TLS (custom & deep hosts)

A wildcard certificate only covers a **single** label level. `*.example.com` is
fine, but browsers reject a multi-level wildcard like `*.*.example.com`, so deep
site hosts (`app.claude.example.com`) served under one throw a security warning.

Caddy's [on-demand TLS](https://caddyserver.com/docs/automatic-https#on-demand-tls)
solves this: instead of a wildcard, Caddy obtains a normal single-name
certificate for each concrete host during the TLS handshake. To keep clients
from making Caddy request certificates for arbitrary hostnames, it first "asks"
an endpoint whether the host is allowed. gotifacts exposes that endpoint and
approves only the apex or a **known live site** — set `GOTIFACTS_ON_DEMAND_TLS=true`
(see [configuration](/gotifacts/reference/configuration/)) to enable it.

```d2
# Recolor the D2 palette to the docs' green brand (see src/styles/custom.css).
vars: {
  d2-config: {
    theme-overrides: {
      B1: "#0c3a1f"; B2: "#11351f"; B3: "#15803d"
      B4: "#1a7f3c"; B5: "#6ee79b"; B6: "#d4f3df"
    }
    dark-theme-overrides: {
      B1: "#d4f3df"; B2: "#b3ecc4"; B3: "#6ee79b"
      B4: "#208a43"; B5: "#11351f"; B6: "#0c3a1f"
    }
  }
}

direction: right

browser: Browser { shape: oval }
ca: "ACME CA\n(Let's Encrypt / ZeroSSL)"

caddy: Caddy {
  handshake: "TLS handshake\nSNI = app.claude.example.com" { shape: diamond }
  ondemand: "on_demand TLS\nno cert cached?"
}

gotifacts: "gotifacts :8080" {
  dispatch: "Dispatch\nintercept ask path\nbefore host routing"
  check: "ask handler\n/_gotifacts/tls-check"
  registry: "SQLite registry\nParseHost + GetSite" { shape: cylinder }
}

browser -> caddy.handshake: 1. GET https://app.claude.example.com
caddy.handshake -> caddy.ondemand: 2. no cached cert
caddy.ondemand -> gotifacts.dispatch: "3. ask ?domain=…"
gotifacts.dispatch -> gotifacts.check: 4. path match (any Host)
gotifacts.check -> gotifacts.registry: 5. known live site?
gotifacts.registry -> caddy.ondemand: 6a. 200 allow
gotifacts.registry -> caddy.ondemand: 6b. 403 deny (unknown)
caddy.ondemand -> ca: 7. issue per-host cert
ca -> caddy.handshake: 8. single-name cert
caddy -> gotifacts: 9. reverse_proxy content
```

Configure the ask endpoint globally, then enable `on_demand` on the deep-host
block instead of relying on a `*.*.example.com` wildcard:

```text
{
    on_demand_tls {
        ask http://gotifacts:8080/_gotifacts/tls-check
    }
}

*.*.example.com {
    import strip_identity
    tls {
        on_demand
    }
    reverse_proxy gotifacts:8080
    header Content-Security-Policy "frame-ancestors https://example.com"
}
```

The apex and one-level `*.example.com` keep their normal wildcard cert. Each
deeper level (up to gotifacts' max depth of 3) needs its own `on_demand` block;
you can also switch `*.example.com` to `on_demand` if you'd rather avoid the ACME
DNS-01 challenge that wildcard certs require.

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
