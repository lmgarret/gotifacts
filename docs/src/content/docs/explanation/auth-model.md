---
title: The two-plane auth model
description: Why gotifacts separates a forward-auth management plane from an API-key ingest plane.
sidebar:
  order: 2
---

gotifacts has two **independent authorization planes**. Understanding the split
explains nearly every routing and security decision in the project.

| Plane | Routes | Authenticated by | Used by |
| --- | --- | --- | --- |
| **Management** | `/`, `/api/*` | a **forward-auth identity header** injected by your proxy | the browser (portal) |
| **Ingest** | `/ingest/*` | a scoped **API key** (`Authorization: Bearer <key>`) | machines (CI, the Claude skill) |

## Why two planes

Humans and machines authenticate differently. A person at a browser is best
authenticated by your existing **SSO** — they shouldn't handle API keys, and no
key should ever live in the browser. A CI job or a bot has no browser session and
shouldn't depend on SSO; it carries a **scoped key**. Conflating the two would
force one mechanism to do a job it's bad at.

So the planes are kept fully separate, down to the proxy routing: `/api/*` sits
behind forward-auth; `/ingest/*` is deliberately left **out** of forward-auth and
guarded only by the API key.

## The identity header

On the management plane, the principal is taken from a forward-auth header
(`GOTIFACTS_FORWARD_AUTH_HEADER`, default `Remote-User`):

- It is honored **only** when the request's direct peer IP is within
  `GOTIFACTS_TRUSTED_PROXIES`. From any other source it is stripped and ignored.
- The principal is that user; they are **admin** iff listed in
  `GOTIFACTS_ADMIN_USERS`.
- Your proxy **must** strip any client-supplied copy of the header before
  injecting the authenticated one — otherwise a client could assert any identity.

This is why a trusted-proxies allowlist is required for the management plane, and
why exposing gotifacts directly to the internet is unsafe.

## The ingest plane

On `/ingest/*` the identity header is irrelevant — only the API key counts. Keys
are **capability-scoped**: each grant binds capabilities (`publish`, `unpublish`,
`rollback`, `patch`) to a target (a group subtree or a single site). See the
[permissions reference](/gotifacts/reference/permissions/).

## Where MCP fits

The optional [MCP connector](/gotifacts/guides/connect-claude-mcp/) spans both
planes by design:

- The browser-facing **consent** step (`/mcp/oauth/authorize`) rides the
  **forward-auth plane** — it's a normal SSO-authenticated page, and it's the
  gate that decides *who* may publish.
- Every machine-facing endpoint (`/mcp`, the token/registration endpoints, the
  `/.well-known/oauth-*` discovery documents) is called server-to-server by
  Claude and sits on the **no-forward-auth plane**, like `/ingest/*`, with OAuth
  (PKCE then a bearer token) as its authentication.

The token Claude receives carries the same grant model as an API key, so the
connector can never act outside what was approved at consent.
