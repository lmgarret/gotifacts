---
title: Frame sites in the portal
description: Let the portal render live, sandboxed thumbnails of your sites with the right CSP header.
sidebar:
  order: 9
---

The portal renders **live, sandboxed iframe thumbnails** of sites. For a site to
be framable, it must be served with a Content-Security-Policy that allows the
apex to frame it:

```http
Content-Security-Policy: frame-ancestors https://<base>
```

## Add the header at the proxy

gotifacts serves the site files; your reverse proxy adds the framing policy to
**site content** responses (the `*.base` / `*.*.base` hosts).

**nginx** — replace any upstream CSP and set the framing policy:

```nginx
proxy_hide_header Content-Security-Policy;
add_header Content-Security-Policy "frame-ancestors https://example.com" always;
```

**Caddy**:

```caddy
header Content-Security-Policy "frame-ancestors https://example.com"
```

See the full [nginx](/gotifacts/guides/reverse-proxy-nginx/) and
[Caddy](/gotifacts/guides/reverse-proxy-caddy/) guides for where this sits.

## Use a static preview instead

If you'd rather not frame a site live — or it sets a stricter CSP itself — set
the `preview` field in the site's metadata to an image URL. The portal uses that
image instead of a live iframe.

```json
{ "group": "claude", "slug": "report", "title": "Report", "preview": "https://.../thumb.png" }
```
