import { useCallback, useEffect, useState } from "react";

// Top-level views, each with a canonical URL. Portal is the app root; the
// admin views live under /settings/. The Go SPA handler
// (internal/portal/spa.go) falls back to index.html for unknown paths, so
// these deep-link and survive a full page reload.
export type View = "portal" | "keys" | "connections" | "trash";

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

const TITLE: Record<View, string> = {
  portal: "gotifacts",
  keys: "API keys · gotifacts",
  connections: "Connections · gotifacts",
  trash: "Trash · gotifacts",
};

// viewFromPath resolves a pathname to a view, defaulting to the portal for the
// root and any unrecognized path.
export function viewFromPath(pathname: string): View {
  const clean = pathname.replace(/\/+$/, "") || "/";
  return PATH_VIEW[clean] ?? "portal";
}

// useView keeps a View in sync with the browser URL. navigate() pushes (or, with
// { replace: true }, replaces) a history entry; Back/Forward is handled via
// popstate. The page title tracks the active view.
export function useView(): {
  view: View;
  navigate: (v: View, opts?: { replace?: boolean }) => void;
} {
  const [view, setView] = useState<View>(() => viewFromPath(window.location.pathname));

  useEffect(() => {
    const onPop = () => setView(viewFromPath(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    document.title = TITLE[view];
  }, [view]);

  const navigate = useCallback((v: View, opts?: { replace?: boolean }) => {
    const path = VIEW_PATH[v];
    if (window.location.pathname !== path) {
      if (opts?.replace) window.history.replaceState(null, "", path);
      else window.history.pushState(null, "", path);
    }
    setView(v);
  }, []);

  return { view, navigate };
}
