import { useEffect, useState } from "react";
import { api, type Me } from "./api";
import { Portal } from "./components/Portal";
import { KeysView } from "./components/KeysView";
import { ConnectionsView } from "./components/ConnectionsView";
import { TrashView } from "./components/TrashView";
import logoLight from "./assets/logo-light.svg";
import logoDark from "./assets/logo-dark.svg";

type View = "portal" | "keys" | "connections" | "trash";

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
  const [view, setView] = useState<View>("portal");
  const [trashCount, setTrashCount] = useState(0);

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch((e: Error) => setError(e.message));
  }, []);

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
        <div className="brand" onClick={() => setView("portal")} role="button" tabIndex={0}>
          <Logo />
        </div>
        <nav>
          <button className={view === "portal" ? "active" : ""} onClick={() => setView("portal")}>
            Portal
          </button>
          {me.is_admin && (
            <button className={view === "keys" ? "active" : ""} onClick={() => setView("keys")}>
              API Keys
            </button>
          )}
          {me.is_admin && me.mcp_enabled && (
            <button
              className={view === "connections" ? "active" : ""}
              onClick={() => setView("connections")}
            >
              Connections
            </button>
          )}
          {me.is_admin && (
            <button
              className={view === "trash" ? "active" : ""}
              onClick={() => setView("trash")}
            >
              Trash{trashCount > 0 && <span className="nav-badge">{trashCount}</span>}
            </button>
          )}
        </nav>
        <div className="who">
          {me.user}
          {me.is_admin && <span className="badge">admin</span>}
        </div>
      </header>
      <main>
        {view === "portal" && <Portal me={me} />}
        {view === "keys" && me.is_admin && <KeysView />}
        {view === "connections" && me.is_admin && me.mcp_enabled && <ConnectionsView />}
        {view === "trash" && me.is_admin && <TrashView onCountChange={setTrashCount} />}
      </main>
    </div>
  );
}
