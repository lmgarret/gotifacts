import { useEffect, useState } from "react";
import { api, type Me } from "./api";
import { Portal } from "./components/Portal";
import { KeysView } from "./components/KeysView";

type View = "portal" | "keys";

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<View>("portal");

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch((e: Error) => setError(e.message));
  }, []);

  if (error) {
    return (
      <div className="centered">
        <h1>gotifacts</h1>
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
          gotifacts
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
        </nav>
        <div className="who">
          {me.user}
          {me.is_admin && <span className="badge">admin</span>}
        </div>
      </header>
      <main>
        {view === "portal" && <Portal me={me} />}
        {view === "keys" && me.is_admin && <KeysView />}
      </main>
    </div>
  );
}
