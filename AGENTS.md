# Agent & contributor guide

Conventions for humans and AI agents working in this repo. For the full
contributor guide see [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Project layout

| Path | What |
| --- | --- |
| `cmd/gotifacts/` | CLI entrypoint: `serve`, `keys`, `mcp` subcommands |
| `internal/` | Core packages (api, auth, ingest, store, router, mcpserver, …) |
| `web/` | Vite + React + TypeScript SPA, embedded via `go:embed` |
| `docs/` | Astro Starlight documentation site (deployed to GitHub Pages) |
| `examples/` | Reference nginx/Caddy configs and the Claude skill |

## Build & test

```sh
go test -race ./...          # backend tests
golangci-lint run ./...      # backend lint
cd web  && npm ci && npm run lint && npm run build   # frontend
cd docs && npm install && npm run build              # docs (fails on broken links)
```

Run all of these before committing changes that touch the corresponding area.

## Keep the API docs in sync (important)

The HTTP API reference is generated from
[`docs/openapi.yaml`](docs/openapi.yaml) by the `starlight-openapi` plugin. It is
**hand-maintained** — there is no code-reflection step, so it does not update
itself.

> **When you add, remove, or change any route or request/response struct in
> `internal/api`, `internal/ingest`, or `internal/store`, you MUST update
> `docs/openapi.yaml` in the same change** — paths, parameters, request bodies,
> response schemas, and the `components.schemas` derived from the Go structs
> (e.g. `store.Site`, `ingest.Meta`, `ingest.Result`, `store.APIKey`,
> `store.Grant`, `store.Connection`, `api.TreeNode`).

Also update any affected prose pages under `docs/src/content/docs/` (for example
`reference/configuration.md` when env vars change, `reference/cli.md` when CLI
flags change, `reference/permissions.md` when the grant model changes).

After editing, verify:

```sh
cd docs && npm run build   # renders the spec + validates internal links
```

## Docs conventions

- The site uses a GitHub Pages **base path** of `/gotifacts`. Internal links in
  Markdown are root-relative **including** the base, e.g.
  `/gotifacts/reference/configuration/`. `starlight-links-validator` enforces
  this at build time.
- Content is organized by [Diátaxis](https://diataxis.fr/): `tutorials/`,
  `guides/` (how-to), `reference/`, `explanation/`.
- **Diagrams: author in [D2](https://d2lang.com), not Mermaid.** Use ```` ```d2 ````
  code blocks — they render to SVG at build time via the D2 binary (a single
  static Go binary, no headless browser). Install it once with `curl -fsSL
  https://d2lang.com/install.sh | sh -s --` so `cd docs && npm run build` can
  render locally; CI installs the same pinned release. D2 gives cleaner layouts
  and a browser-free build; new diagrams should be D2 and existing Mermaid should
  be migrated when touched. See `docs/README.md`.
- **Moving to a custom domain later:** in `docs/astro.config.mjs` set `site` to
  the domain and remove `base`, add `docs/public/CNAME` containing the domain,
  update the `starlight-links-validator` `exclude` entries, and find-replace the
  `/gotifacts/` link prefix across `docs/src/content/`.

## Commit / PR

- Keep changes focused; include tests for new behavior.
- Don't commit build output (`web/dist`, `docs/dist` are gitignored).
- Contributions are licensed under MIT.
- **Never mention AI coding agents, assistant sessions, or AI tooling in commit messages or PR titles/descriptions.** No "Generated with Claude Code", session URLs, `Co-Authored-By: Claude` trailers, or similar attributions.
