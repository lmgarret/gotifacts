import { useEffect, useMemo, useState, type FormEvent } from "react";
import {
  api,
  CAPABILITIES,
  type ApiKey,
  type Capability,
  type CreatedKey,
  type Grant,
  type Site,
} from "../api";
import { hostForDir, impact, TargetSelect } from "./TargetSelect";

function emptyGrant(): Grant {
  return { kind: "group", target: "", permissions: ["publish"] };
}

// deriveTargets splits the site list into the set of existing site dirs and the
// set of group prefixes that contain them.
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
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const [allSites, setAllSites] = useState<Site[]>([]);
  const [base, setBase] = useState("");

  const [name, setName] = useState("");
  const [admin, setAdmin] = useState(false);
  const [grants, setGrants] = useState<Grant[]>([emptyGrant()]);

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

  const resetForm = () => {
    setName("");
    setAdmin(false);
    setGrants([emptyGrant()]);
  };

  const updateGrant = (i: number, patch: Partial<Grant>) =>
    setGrants((gs) => gs.map((g, j) => (j === i ? { ...g, ...patch } : g)));

  const toggleCap = (i: number, cap: Capability) =>
    setGrants((gs) =>
      gs.map((g, j) => {
        if (j !== i) return g;
        const has = g.permissions.includes(cap);
        return {
          ...g,
          permissions: has
            ? g.permissions.filter((c) => c !== cap)
            : [...g.permissions, cap],
        };
      }),
    );

  const addGrant = () => setGrants((gs) => [...gs, emptyGrant()]);
  const removeGrant = (i: number) =>
    setGrants((gs) => (gs.length > 1 ? gs.filter((_, j) => j !== i) : gs));

  // Client-side validation mirroring the server invariants.
  const validationError = (): string | null => {
    if (admin) return null;
    if (grants.length === 0) return "Add at least one grant or make the key an admin.";
    for (const g of grants) {
      if (g.permissions.length === 0) return "Each grant needs at least one capability.";
      if (g.kind === "site" && !g.target.trim()) return "A site grant needs a target.";
    }
    return null;
  };

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const v = validationError();
    if (v) {
      setError(v);
      return;
    }
    try {
      const k = await api.createKey({
        name,
        admin,
        grants: admin ? [] : grants.map((g) => ({ ...g, target: g.target.trim() })),
      });
      setCreated(k);
      resetForm();
      load();
    } catch (err) {
      setError((err as Error).message);
    }
  };

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
      <h2>API Keys</h2>
      <p className="muted">
        Keys authorize the machine ingest plane (<code>/ingest/*</code>). The portal itself never
        uses a key — your proxy authenticates you. A key is either an <strong>admin</strong>{" "}
        superuser, or a set of <strong>grants</strong> giving capabilities on a group subtree or a
        single site.
      </p>

      {created && (
        <div className="newkey">
          <strong>
            New {created.admin ? "admin " : ""}key “{created.name}” created.
          </strong>
          <p>Copy it now — it is shown only once:</p>
          <code className="token">{created.key}</code>
          <button onClick={() => setCreated(null)}>Done</button>
        </div>
      )}

      <form className="keyform" onSubmit={create}>
        <div className="keyform-row">
          <input
            placeholder="Key name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
          <label className="checkbox">
            <input
              type="checkbox"
              checked={admin}
              onChange={(e) => setAdmin(e.target.checked)}
            />
            admin (full access)
          </label>
        </div>

        {!admin && (
          <div className="grants-editor">
            {grants.map((g, i) => (
              <div className="grant-row" key={i}>
                <TargetSelect
                  kind={g.kind}
                  target={g.target}
                  groups={groups}
                  sites={sites}
                  base={base}
                  onChange={(kind, target) => updateGrant(i, { kind, target })}
                />
                <div className="caps">
                  {CAPABILITIES.map((cap) => (
                    <label className="checkbox" key={cap}>
                      <input
                        type="checkbox"
                        checked={g.permissions.includes(cap)}
                        onChange={() => toggleCap(i, cap)}
                      />
                      {cap}
                    </label>
                  ))}
                </div>
                <button
                  type="button"
                  className="small"
                  onClick={() => removeGrant(i)}
                  disabled={grants.length === 1}
                  title="Remove grant"
                >
                  ✕
                </button>
                {base && (
                  <span className="grant-impact muted" title={impact(g.kind, g.target, base)}>
                    {impact(g.kind, g.target, base)}
                  </span>
                )}
              </div>
            ))}
            <button type="button" className="small" onClick={addGrant}>
              + Add grant
            </button>
          </div>
        )}

        <button type="submit">Create key</button>
      </form>

      {error && <p className="error">{error}</p>}

      <table className="keytable">
        <thead>
          <tr>
            <th>Name</th>
            <th>Access</th>
            <th>Created</th>
            <th>Last used</th>
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
                    {k.grants.map((g, i) => (
                      <li key={i} title={base ? impact(g.kind, g.target, base) : undefined}>
                        <span className={`target-badge ${g.kind} ${g.target === "" && g.kind === "group" ? "global" : ""}`}>
                          {g.target === "" && g.kind === "group" ? "global" : g.kind}
                        </span>
                        <code>{g.target || (base ? hostForDir("", base) : "*")}</code>
                        <span className="muted"> → </span>
                        {g.permissions.join(", ")}
                      </li>
                    ))}
                  </ul>
                )}
              </td>
              <td>{new Date(k.created_at).toLocaleDateString()}</td>
              <td>{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "never"}</td>
              <td>
                <button className="danger small" onClick={() => revoke(k.id)}>
                  Revoke
                </button>
              </td>
            </tr>
          ))}
          {keys.length === 0 && (
            <tr>
              <td colSpan={5} className="muted">
                No keys yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
