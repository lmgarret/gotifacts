// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import starlightOpenAPI, { openAPISidebarGroups } from 'starlight-openapi';
import starlightLlmsTxt from 'starlight-llms-txt';
import starlightImageZoom from 'starlight-image-zoom';
import starlightLinksValidator from 'starlight-links-validator';

// https://astro.build/config
export default defineConfig({
  // GitHub Pages project page. To move to a custom domain later: set `site` to
  // the domain (e.g. 'https://docs.gotifacts.dev'), delete `base`, and add a
  // `public/CNAME` file containing the domain.
  site: 'https://lmgarret.github.io',
  base: '/gotifacts',
  integrations: [
    starlight({
      title: 'gotifacts',
      description:
        'A single, self-hosted Go service that hosts static sites by host-based routing and serves a dynamic portal to browse them.',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/lmgarret/gotifacts',
        },
      ],
      editLink: {
        baseUrl: 'https://github.com/lmgarret/gotifacts/edit/main/docs/',
      },
      plugins: [
        // Auto-render the HTTP API reference from the hand-authored spec.
        starlightOpenAPI([
          {
            base: 'reference/api',
            label: 'HTTP API',
            schema: './openapi.yaml',
          },
        ]),
        // Emit /llms.txt and /llms-full.txt so LLMs can ingest the docs.
        starlightLlmsTxt(),
        // Click-to-zoom on diagrams and screenshots.
        starlightImageZoom(),
        // Fail the build on broken internal links (CI quality gate).
        // The `reference/api/**` routes are injected by starlight-openapi and
        // aren't in the validator's known-page set, so links into that section
        // are excluded from validation.
        starlightLinksValidator({
          exclude: ['/gotifacts/reference/api', '/gotifacts/reference/api/**'],
        }),
      ],
      sidebar: [
        { label: 'Tutorials', items: [{ autogenerate: { directory: 'tutorials' } }] },
        { label: 'How-to guides', items: [{ autogenerate: { directory: 'guides' } }] },
        {
          label: 'Reference',
          items: [
            { label: 'Configuration', slug: 'reference/configuration' },
            { label: 'CLI', slug: 'reference/cli' },
            { label: 'URL ⇄ path convention', slug: 'reference/url-path-convention' },
            { label: 'Permissions', slug: 'reference/permissions' },
            ...openAPISidebarGroups,
          ],
        },
        { label: 'Explanation', items: [{ autogenerate: { directory: 'explanation' } }] },
      ],
    }),
    sitemap(),
  ],
});
