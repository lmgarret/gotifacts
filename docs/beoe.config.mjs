// Cache location for @beoe build-time Mermaid SVGs. Committed to the repo so CI
// renders from cache and never needs a headless browser. Regenerate locally
// (see docs/README.md) when you add or edit a diagram.
export default {
  database: new URL('.beoe/cache.sqlite', import.meta.url).pathname,
};
