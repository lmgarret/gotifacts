# gotifacts docs

The gotifacts documentation site, built with [Astro
Starlight](https://starlight.astro.build/) and organized by the
[Diátaxis](https://diataxis.fr/) framework. Deployed to GitHub Pages by
[`.github/workflows/docs.yml`](../.github/workflows/docs.yml).

## Develop

```sh
npm install
npm run dev      # http://localhost:4321/gotifacts
npm run build    # production build; fails on broken internal links
npm run preview  # preview the production build
```

## Structure

- `src/content/docs/{tutorials,guides,reference,explanation}/` — the four
  Diátaxis quadrants.
- `openapi.yaml` — hand-maintained OpenAPI spec; rendered to
  `reference/api/*` by `starlight-openapi`. **Keep it in sync with the Go HTTP
  layer** — see [`../AGENTS.md`](../AGENTS.md).
- `astro.config.mjs` — site config, plugins, and the sidebar.

## Diagrams (build-time D2)

**Author all diagrams in [D2](https://d2lang.com), not Mermaid.** Diagrams are
```` ```d2 ```` code blocks rendered to static SVG at build time by
[`astro-d2`](https://astro-d2.vercel.app/), which shells out to the **D2 binary**
— a single static Go binary, no headless browser. The rendered SVG is inlined in
the page; its dark variant rides along inside the SVG's own
`prefers-color-scheme: dark` media query.

Building locally needs the D2 binary on your `PATH` (the
[`D2_VERSION`](../.github/workflows/docs.yml) pinned in CI):

```sh
curl -fsSL https://d2lang.com/install.sh | sh -s --   # one-time
npm run build
```

CI ([`.github/workflows/docs.yml`](../.github/workflows/docs.yml)) installs the
same pinned D2 release before building. Generated SVGs land in `public/d2/`,
which is gitignored.

## Custom domain

The site currently deploys to a GitHub Pages project page (`base: '/gotifacts'`).
To move to a custom domain: set `site` to the domain in `astro.config.mjs`,
remove `base`, add a `public/CNAME` file containing the domain, point DNS at
GitHub Pages, and update the link prefixes (see `../AGENTS.md`).
