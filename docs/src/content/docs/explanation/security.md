---
title: Security & threat model
description: The assumptions gotifacts makes, the risks it defends against, and how to report issues.
sidebar:
  order: 3
---

gotifacts is designed to run **behind an operator-provided reverse proxy** that
terminates TLS and performs forward-auth/SSO. Its security posture follows from
that assumption. The canonical policy is
[`SECURITY.md`](https://github.com/lmgarret/gotifacts/blob/main/SECURITY.md).

## Key properties

- **Never expose gotifacts directly to the internet.** It serves plain HTTP and
  trusts a forward-auth identity header. Direct exposure would let anyone assert
  any identity.
- **Identity-header spoofing is the primary risk.** The header is honored only
  when the request's direct peer IP is within `GOTIFACTS_TRUSTED_PROXIES`;
  otherwise it is stripped. Your proxy must strip any client-supplied copy before
  injecting the real one. (See the [auth model](/gotifacts/explanation/auth-model/).)
- **The ingest plane is authorized only by scoped API keys**, which are hashed at
  rest (SHA-256), shown in plaintext once, compared in constant time, and never
  logged.
- **Uploads are guarded** against zip-slip, symlink escapes, tar-bombs, and
  oversized payloads. Sites are written to a temp dir on the same volume,
  validated, then atomically swapped into place.

## What the proxy must do

The threat model leans on the proxy to:

1. Terminate TLS (gotifacts never speaks TLS).
2. Authenticate management-plane users via SSO and inject the identity header.
3. **Strip any client-supplied identity header** before injecting the real one.
4. Leave `/ingest/*` and the MCP machine endpoints *out* of forward-auth.

The [nginx](/gotifacts/guides/reverse-proxy-nginx/) and
[Caddy](/gotifacts/guides/reverse-proxy-caddy/) guides implement exactly this.

## Reporting a vulnerability

Report security issues **privately** via GitHub's private vulnerability
reporting:

➡️ **https://github.com/lmgarret/gotifacts/security/advisories/new**

Do not open a public issue for security problems. If you find a way to bypass any
of the protections above, please report it.
