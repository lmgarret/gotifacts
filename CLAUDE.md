# CLAUDE.md

See @AGENTS.md for repo conventions, build/test commands, and the docs layout.

**Most important rule:** the HTTP API reference is generated from
`docs/openapi.yaml`, which is hand-maintained. Whenever you change a route or a
request/response struct in `internal/api`, `internal/ingest`, or
`internal/store`, update `docs/openapi.yaml` (and any affected `docs/` pages) in
the same change, then run `cd docs && npm run build` to verify. Details in
@AGENTS.md.

**Docs diagrams:** author them in [D2](https://d2lang.com) (```` ```d2 ````
blocks), not Mermaid — D2 renders to SVG with a single static binary (no headless
browser) and lays out more cleanly. See `docs/README.md`.
