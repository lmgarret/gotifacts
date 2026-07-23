import { useEffect, useState } from "react";
import { api, type Me, type Site } from "./api";
import { Portal } from "./components/Portal";
import { SitePage, type Tab } from "./components/SitePage";
import { KeysView } from "./components/KeysView";
import { ConnectionsView } from "./components/ConnectionsView";
import { TrashView } from "./components/TrashView";
import { AccountMenu } from "./components/AccountMenu";
import { useRoute } from "./router";
import logoLight from "./assets/logo-light.svg";
import logoDark from "./assets/logo-dark.svg";

// Light/dark wordmark logos, matching the docs site. CSS swaps which one is
// shown based on the active color scheme.
function Logo() {
  return (
    <>
      <img className="logo logo-light" src={logoLight} alt="gotifacts" />
      <img className="logo logo-dark" src={logoDark} alt="gotifacts" />
    </>
  );
}

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { route, navigate, openSite } = useRoute();
  const view = route.view;
  const siteRef = route.site;
  const [trashCount, setTrashCount] = useState(0);
  // The resolved Site backing a /s/ deep link. When the user opens a site from
  // the portal we already hold the object; on a cold load we fetch it below.
  const [site, setSite] = useState<Site | null>(null);
  // Set (keyed to the failing site path) when a /s/ deep link can't be
  // resolved, so we can show a "not found" notice instead of bouncing away.
  const [siteErr, setSiteErr] = useState<{ group: string; slug: string } | null>(null);

  // openSiteFromPortal navigates to a site's page, stashing the object the
  // portal already has so SitePage renders without a round-trip.
  const openSiteFromPortal = (s: Site) => {
    setSite(s);
    openSite({ group: s.group, slug: s.slug });
  };

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch((e: Error) => setError(e.message));
  }, []);

  // Resolve the site behind a /s/ route. Skip the fetch when we already hold the
  // matching object (opened from the portal); on a cold load or an unknown site
  // fetch it. A failure records the error against this path so the render shows
  // a "not found" notice rather than silently redirecting.
  useEffect(() => {
    if (!siteRef) return;
    if (site && site.group === siteRef.group && site.slug === siteRef.slug) return;
    let cancelled = false;
    api
      .getSite(siteRef.group, siteRef.slug)
      .then((s) => {
        if (!cancelled) {
          setSite(s);
          setSiteErr(null);
        }
      })
      .catch(() => {
        if (!cancelled) setSiteErr({ group: siteRef.group, slug: siteRef.slug });
      });
    return () => {
      cancelled = true;
    };
  }, [siteRef, site]);

  // Deep links to a settings view are only valid for users who can see that
  // view. Coerce anything else back to the portal, correcting the URL in place
  // so a shared/bookmarked link never lands on a blank screen.
  useEffect(() => {
    if (!me) return;
    const allowed =
      view === "portal" ||
      (me.is_admin && (view === "keys" || view === "trash")) ||
      (me.is_admin && me.mcp_enabled && view === "connections");
    if (!allowed) navigate("portal", { replace: true });
  }, [me, view, navigate]);

  if (error) {
    return (
      <div className="centered">
        <div className="brand-logo">
          <Logo />
        </div>
        <p className="error">Could not authenticate: {error}</p>
        <p className="muted">Ensure you are reaching the portal through your authenticating proxy.</p>
      </div>
    );
  }

  if (!me) {
    return (
      <div className="centered">
        <p className="muted">Loading…</p>
      </div>
    );
  }

  return (
    <div className="app">
      <header className="topbar">
        <div
          className="brand"
          onClick={() => navigate("portal")}
          onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && navigate("portal")}
          role="button"
          tabIndex={0}
          title="Home"
          aria-label="Home"
        >
          <Logo />
        </div>
        <a
          className="docs-link"
          href="https://lmgarret.github.io/gotifacts/"
          target="_blank"
          rel="noopener noreferrer"
        >
          Docs ↗
        </a>
        <AccountMenu me={me} view={view} trashCount={trashCount} onNavigate={navigate} />
      </header>
      <main>
        {siteRef ? (
          site && site.group === siteRef.group && site.slug === siteRef.slug ? (
            <SitePage
              site={site}
              tab={route.sub[0] === "files" ? "files" : "overview"}
              onTabChange={(t: Tab) =>
                openSite(siteRef, { sub: t === "overview" ? [] : [t] })
              }
              base={me.base_domain}
              isAdmin={me.is_admin}
              versioningEnabled={me.versioning_enabled ?? false}
              onBack={() => navigate("portal")}
              onGone={() => navigate("portal", { replace: true })}
            />
          ) : siteErr && siteErr.group === siteRef.group && siteErr.slug === siteRef.slug ? (
            <div className="centered">
              <p className="error">Site not found</p>
              <p className="muted">
                No site is published at{" "}
                <code>{siteRef.group ? `${siteRef.group}/${siteRef.slug}` : siteRef.slug}</code>, or
                it is hidden from your account.
              </p>
              <button onClick={() => navigate("portal")}>← Back to portal</button>
            </div>
          ) : (
            <p className="muted">Loading…</p>
          )
        ) : (
          <>
            {view === "portal" && <Portal me={me} onOpenSite={openSiteFromPortal} />}
            {view === "keys" && me.is_admin && <KeysView />}
            {view === "connections" && me.is_admin && me.mcp_enabled && <ConnectionsView />}
            {view === "trash" && me.is_admin && <TrashView onCountChange={setTrashCount} />}
          </>
        )}
      </main>
    </div>
  );
}
