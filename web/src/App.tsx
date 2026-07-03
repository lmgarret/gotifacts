import { useEffect, useState } from "react";
import { api, type Me, type Site } from "./api";
import { Portal } from "./components/Portal";
import { SitePage } from "./components/SitePage";
import { KeysView } from "./components/KeysView";
import { ConnectionsView } from "./components/ConnectionsView";
import { TrashView } from "./components/TrashView";
import { AccountMenu } from "./components/AccountMenu";
import { useView, type View } from "./router";
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
  const { view, navigate } = useView();
  const [trashCount, setTrashCount] = useState(0);
  // When set (within the portal view), the dedicated per-site page is shown.
  const [openSite, setOpenSite] = useState<Site | null>(null);

  // go switches the top-level view (updating the URL) and leaves any open site.
  const go = (v: View) => {
    setOpenSite(null);
    navigate(v);
  };

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch((e: Error) => setError(e.message));
  }, []);

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
          onClick={() => go("portal")}
          onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && go("portal")}
          role="button"
          tabIndex={0}
          title="Home"
          aria-label="Home"
        >
          <Logo />
        </div>
        <AccountMenu me={me} view={view} trashCount={trashCount} onNavigate={go} />
      </header>
      <main>
        {view === "portal" && !openSite && <Portal me={me} onOpenSite={setOpenSite} />}
        {view === "portal" && openSite && (
          <SitePage
            site={openSite}
            base={me.base_domain}
            isAdmin={me.is_admin}
            versioningEnabled={me.versioning_enabled ?? false}
            onBack={() => setOpenSite(null)}
            onGone={() => setOpenSite(null)}
          />
        )}
        {view === "keys" && me.is_admin && <KeysView />}
        {view === "connections" && me.is_admin && me.mcp_enabled && <ConnectionsView />}
        {view === "trash" && me.is_admin && <TrashView onCountChange={setTrashCount} />}
      </main>
    </div>
  );
}
