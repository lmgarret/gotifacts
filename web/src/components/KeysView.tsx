import { useEffect, useMemo, useState } from "react";
import { api, type ApiKey, type Site } from "../api";
import { hostForDir, scopeImpact, scopeTypeOf } from "../grants";
import { KeyCreateModal } from "./KeyCreateModal";

// deriveTargets splits the site list into the set of existing site dirs and the
// set of group prefixes that contain them, for the create modal's suggestions.
function deriveTargets(sites: Site[]): { groups: string[]; sites: string[] } {
  const groups = new Set<string>();
  const siteDirs = new Set<string>();
  for (const s of sites) {
    const dir = s.group ? `${s.group}/${s.slug}` : s.slug;
    siteDirs.add(dir);
    const segs = dir.split("/");
    for (let i = 1; i < segs.length; i++) groups.add(segs.slice(0, i).join("/"));
  }
  return { groups: [...groups].sort(), sites: [...siteDirs].sort() };
}

export function KeysView() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [allSites, setAllSites] = useState<Site[]>([]);
  const [base, setBase] = useState("");
  const [creating, setCreating] = useState(false);

  const { groups, sites } = useMemo(() => deriveTargets(allSites), [allSites]);

  const load = () => {
    api
      .listKeys()
      .then((r) => setKeys(r.keys))
      .catch((e: Error) => setError(e.message));
  };

  useEffect(() => {
    load();
    api.me().then((m) => setBase(m.base_domain)).catch(() => {});
    api.listSites().then((r) => setAllSites(r.sites)).catch(() => {});
  }, []);

  const revoke = async (id: number) => {
    if (!confirm("Revoke this key? Clients using it will stop working.")) return;
    try {
      await api.deleteKey(id);
      load();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <div className="keys">
      <div className="keys-head">
        <h2>API Keys</h2>
        <button onClick={() => setCreating(true)}>+ New key</button>
      </div>
      <p className="muted">
        Keys authorize the machine ingest plane (<code>/ingest/*</code>). The portal itself never
        uses a key — your proxy authenticates you. A key is either an <strong>admin</strong>{" "}
        superuser, or a set of <strong>grants</strong> giving capabilities on a group, a single
        site, or all sites.
      </p>

      {error && <p className="error">{error}</p>}

      <table className="keytable">
        <thead>
          <tr>
            <th>Name</th>
            <th>Access</th>
            <th>Created</th>
            <th>Last used</th>
            <th>Expires</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {keys.map((k) => (
            <tr key={k.id}>
              <td>{k.name}</td>
              <td>
                {k.admin ? (
                  <span className="tag admin">admin</span>
                ) : k.grants.length === 0 ? (
                  <span className="muted">—</span>
                ) : (
                  <ul className="grant-list">
                    {k.grants.map((g, i) => {
                      const type = scopeTypeOf(g);
                      return (
                        <li key={i} title={base ? scopeImpact(type, g.target, base) : undefined}>
                          <span className={`target-badge ${type}`}>{type}</span>
                          <code>{type === "global" ? "all sites" : g.target || (base ? hostForDir("", base) : "*")}</code>
                          <span className="muted"> → </span>
                          {g.permissions.join(", ")}
                        </li>
                      );
                    })}
                  </ul>
                )}
              </td>
              <td>{new Date(k.created_at).toLocaleDateString()}</td>
              <td>{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "never"}</td>
              <td>
                {!k.expires_at ? (
                  <span className="muted">never</span>
                ) : new Date(k.expires_at) < new Date() ? (
                  <span className="tag warn">expired</span>
                ) : (
                  new Date(k.expires_at).toLocaleDateString()
                )}
              </td>
              <td>
                <button className="danger small" onClick={() => revoke(k.id)}>
                  Revoke
                </button>
              </td>
            </tr>
          ))}
          {keys.length === 0 && (
            <tr>
              <td colSpan={6} className="muted">
                No keys yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>

      {creating && (
        <KeyCreateModal
          groups={groups}
          sites={sites}
          base={base}
          onClose={() => setCreating(false)}
          onCreated={load}
        />
      )}
    </div>
  );
}
