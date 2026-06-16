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
SVG at build time by `@beoe/rehype-mermaid`. The rendered SVGs are cached in
`.beoe/cache.sqlite`, **which is committed** so CI builds from cache and never
launches or downloads a browser.

When you add or edit a diagram, regenerate the cache locally (needs a Chromium
matching the pinned `playwright` version):

```sh
npx playwright install chromium   # one-time, downloads the matching browser
rm -rf .beoe && npm run build      # re-renders and re-seeds the cache
git add .beoe                      # commit the refreshed cache
```

> The cache key is tied to the diagram source **and** the pinned `playwright`
> version. If you bump `playwright`, regenerate the cache in the same change,
> otherwise CI (which has no browser) will fail to render.

## Custom domain

The site currently deploys to a GitHub Pages project page (`base: '/gotifacts'`).
To move to a custom domain: set `site` to the domain in `astro.config.mjs`,
remove `base`, add a `public/CNAME` file containing the domain, point DNS at
GitHub Pages, and update the link prefixes (see `../AGENTS.md`).
