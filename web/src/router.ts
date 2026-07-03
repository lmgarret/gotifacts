import { useCallback, useEffect, useState } from "react";

// Top-level views, each with a canonical URL. Portal is the app root; the
// admin views live under /settings/. Individual sites deep-link under /s/. The
// Go SPA handler (internal/portal/spa.go) falls back to index.html for unknown
// paths, so all of these survive a full page reload.
export type View = "portal" | "keys" | "connections" | "trash";

// SiteRef identifies a site by its path (group may be empty for a flat site).
export interface SiteRef {
  group: string;
  slug: string;
}

// A Route is either a top-level view or, when site is set, a site's page. The
// site page conceptually sits over the portal, so its view stays "portal". sub
// holds the per-site subpath (e.g. the active tab) — the segments after the
// reserved "-" boundary under /s/; empty means the site's default page.
export interface Route {
  view: View;
  site: SiteRef | null;
  sub: string[];
}

export const VIEW_PATH: Record<View, string> = {
  portal: "/",
  keys: "/settings/api-keys",
  connections: "/settings/connections",
  trash: "/settings/trash",
};

const PATH_VIEW: Record<string, View> = {
  "/settings/api-keys": "keys",
  "/settings/connections": "connections",
  "/settings/trash": "trash",
};

const SITE_PREFIX = "/s/";
// Reserved segment separating a site's identity path from its per-site subpath.
// A group/slug label can never be "-" (labels must start and end alphanumeric),
// so this boundary can never collide with a site or group name. Mirrors the
// GitLab "/-/" convention.
const SITE_SUB_SEP = "-";

const VIEW_TITLE: Record<View, string> = {
  portal: "gotifacts",
  keys: "API keys · gotifacts",
  connections: "Connections · gotifacts",
  trash: "Trash · gotifacts",
};

// sitePathTail joins a SiteRef into the group/slug tail used under /s/.
function sitePathTail(ref: SiteRef): string {
  return ref.group ? `${ref.group}/${ref.slug}` : ref.slug;
}

// routeToPath renders a Route to its canonical URL.
export function routeToPath(r: Route): string {
  if (r.site) {
    let p = SITE_PREFIX + sitePathTail(r.site);
    if (r.sub.length > 0) p += `/${SITE_SUB_SEP}/${r.sub.join("/")}`;
    return p;
  }
  return VIEW_PATH[r.view];
}

// routeFromPath resolves a pathname to a Route, defaulting to the portal for
// the root and any unrecognized path. Under /s/, the identity path (group +
// slug) is everything before the reserved "-" segment; anything after it is the
// per-site subpath.
export function routeFromPath(pathname: string): Route {
  const clean = pathname.replace(/\/+$/, "") || "/";
  if (clean.startsWith(SITE_PREFIX)) {
    const segs = clean.slice(SITE_PREFIX.length).split("/").filter(Boolean);
    const sep = segs.indexOf(SITE_SUB_SEP);
    const idSegs = sep === -1 ? segs : segs.slice(0, sep);
    const sub = sep === -1 ? [] : segs.slice(sep + 1);
    if (idSegs.length >= 1) {
      const slug = idSegs[idSegs.length - 1];
      const group = idSegs.slice(0, -1).join("/");
      return { view: "portal", site: { group, slug }, sub };
    }
    return { view: "portal", site: null, sub: [] };
  }
  return { view: PATH_VIEW[clean] ?? "portal", site: null, sub: [] };
}

function titleFor(r: Route): string {
  return r.site ? `${r.site.slug} · gotifacts` : VIEW_TITLE[r.view];
}

// useRoute keeps a Route in sync with the browser URL. navigate() switches to a
// top-level view; openSite() opens a site page. Both push a history entry (or
// replace it with { replace: true }); Back/Forward is handled via popstate, and
// the page title tracks the active route.
export function useRoute(): {
  route: Route;
  navigate: (view: View, opts?: { replace?: boolean }) => void;
  openSite: (site: SiteRef, opts?: { sub?: string[]; replace?: boolean }) => void;
} {
  const [route, setRoute] = useState<Route>(() => routeFromPath(window.location.pathname));

  useEffect(() => {
    const onPop = () => setRoute(routeFromPath(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    document.title = titleFor(route);
  }, [route]);

  const push = useCallback((next: Route, opts?: { replace?: boolean }) => {
    const path = routeToPath(next);
    if (window.location.pathname !== path) {
      if (opts?.replace) window.history.replaceState(null, "", path);
      else window.history.pushState(null, "", path);
    }
    setRoute(next);
  }, []);

  const navigate = useCallback(
    (view: View, opts?: { replace?: boolean }) => push({ view, site: null, sub: [] }, opts),
    [push],
  );
  const openSite = useCallback(
    (site: SiteRef, opts?: { sub?: string[]; replace?: boolean }) =>
      push({ view: "portal", site, sub: opts?.sub ?? [] }, opts),
    [push],
  );

  return { route, navigate, openSite };
}
