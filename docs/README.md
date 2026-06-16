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

## Diagrams (build-time Mermaid)

Diagrams are authored as ```` ```mermaid ```` code blocks and rendered to static
SVG at build time by `@beoe/rehype-mermaid`, which drives a headless Chromium
through Playwright. Both `npm run dev` and `npm run build` render them on the
fly, so building the docs locally needs a Chromium installed once:

```sh
npx playwright install chromium   # one-time, downloads the matching browser
npm run build
```

CI ([`.github/workflows/docs.yml`](../.github/workflows/docs.yml)) installs
Chromium with `npx playwright install --with-deps chromium` before building, so
diagrams render in GitHub Actions the same way they do locally — no cache to
keep in sync.

## Custom domain

The site currently deploys to a GitHub Pages project page (`base: '/gotifacts'`).
To move to a custom domain: set `site` to the domain in `astro.config.mjs`,
remove `base`, add a `public/CNAME` file containing the domain, point DNS at
GitHub Pages, and update the link prefixes (see `../AGENTS.md`).
