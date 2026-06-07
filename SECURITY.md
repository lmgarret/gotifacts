# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately** using GitHub's private
vulnerability reporting:

➡️ **https://github.com/lmgarret/gotifacts/security/advisories/new**

Do not open a public issue for security problems. We aim to acknowledge reports
within a few days and will coordinate a fix and disclosure timeline with you.

## Scope and threat model

gotifacts is designed to run **behind an operator-provided reverse proxy** that
terminates TLS and performs forward-auth/SSO. Key properties:

- **Never expose gotifacts directly to the internet.** It serves plain HTTP and
  trusts a forward-auth identity header.
- **Identity-header spoofing is the primary risk.** The header is honored only
  when the request's direct peer IP is within `GOTIFACTS_TRUSTED_PROXIES`;
  otherwise it is stripped and ignored. Your proxy must strip any
  client-supplied copy of the identity header before injecting the real one.
- The ingest plane (`/ingest/*`) is authorized only by scoped API keys, which
  are hashed at rest (SHA-256), shown in plaintext once, and never logged.
- Uploads are guarded against zip-slip, symlink escapes, tar-bombs, and
  oversized payloads.

If you find a way to bypass any of these protections, please report it.
