import { useEffect, useState } from "react";
import { api, type Connection } from "../api";
import { hostForDir, scopeImpact, scopeTypeOf } from "../grants";

// ConnectionsView lists active MCP connector authorizations (OAuth consents) and
// lets an admin revoke them, so a connected Claude account can be cut off.
export function ConnectionsView() {
  const [conns, setConns] = useState<Connection[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [base, setBase] = useState("");

  const load = () => {
    api
      .listConnections()
      .then((r) => setConns(r.connections))
      .catch((e: Error) => setError(e.message));
  };

  useEffect(() => {
    load();
    api.me().then((m) => setBase(m.base_domain)).catch(() => {});
  }, []);

  const revoke = async (c: Connection) => {
    const who = c.client_name || c.client_id;
    if (!confirm(`Revoke the connection from "${who}"? It will lose access immediately.`)) return;
    try {
      await api.revokeConnection(c.id);
      load();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <div className="keys">
      <div className="keys-head">
        <h2>Connections</h2>
      </div>
      <p className="muted">
        Each row is an MCP connector (e.g. a Claude account) you authorized via OAuth. Revoking one
        deletes its tokens, so the connector loses access immediately. Connectors are scoped by the
        same grant model as API keys.
      </p>

      {error && <p className="error">{error}</p>}

      <table className="keytable">
        <thead>
          <tr>
            <th>Client</th>
            <th>User</th>
            <th>Access</th>
            <th>Created</th>
            <th>Last used</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {conns.map((c) => (
            <tr key={c.id}>
              <td>{c.client_name || c.client_id}</td>
              <td>{c.user}</td>
              <td>
                {c.grants.length === 0 ? (
                  <span className="muted">—</span>
                ) : (
                  <ul className="grant-list">
                    {c.grants.map((g, i) => {
                      const type = scopeTypeOf(g);
                      return (
                        <li key={i} title={base ? scopeImpact(type, g.target, base) : undefined}>
                          <span className={`target-badge ${type}`}>{type}</span>
                          <code>
                            {type === "global" ? "all sites" : g.target || (base ? hostForDir("", base) : "*")}
                          </code>
                          <span className="muted"> → </span>
                          {g.permissions.join(", ")}
                        </li>
                      );
                    })}
                  </ul>
                )}
              </td>
              <td>{new Date(c.created_at).toLocaleDateString()}</td>
              <td>{c.last_used_at ? new Date(c.last_used_at).toLocaleString() : "never"}</td>
              <td>
                <button className="danger small" onClick={() => revoke(c)}>
                  Revoke
                </button>
              </td>
            </tr>
          ))}
          {conns.length === 0 && (
            <tr>
              <td colSpan={6} className="muted">
                No active connections.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
