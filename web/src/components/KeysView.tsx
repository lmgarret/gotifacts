import { useEffect, useState, type FormEvent } from "react";
import { api, type ApiKey, type CreatedKey } from "../api";

export function KeysView() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<CreatedKey | null>(null);

  const [name, setName] = useState("");
  const [scope, setScope] = useState("publish");
  const [group, setGroup] = useState("");

  const load = () => {
    api
      .listKeys()
      .then((r) => setKeys(r.keys))
      .catch((e: Error) => setError(e.message));
  };

  useEffect(load, []);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      const k = await api.createKey(name, scope, scope === "publish" ? group : "");
      setCreated(k);
      setName("");
      setGroup("");
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
        uses a key — your proxy authenticates you.
      </p>

      {created && (
        <div className="newkey">
          <strong>New {created.scope} key “{created.name}” created.</strong>
          <p>Copy it now — it is shown only once:</p>
          <code className="token">{created.key}</code>
          <button onClick={() => setCreated(null)}>Done</button>
        </div>
      )}

      <form className="keyform" onSubmit={create}>
        <input
          placeholder="Key name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
        <select value={scope} onChange={(e) => setScope(e.target.value)}>
          <option value="publish">publish</option>
          <option value="admin">admin</option>
        </select>
        {scope === "publish" && (
          <input
            placeholder="group restriction (optional)"
            value={group}
            onChange={(e) => setGroup(e.target.value)}
          />
        )}
        <button type="submit">Create key</button>
      </form>

      {error && <p className="error">{error}</p>}

      <table className="keytable">
        <thead>
          <tr>
            <th>Name</th>
            <th>Scope</th>
            <th>Group</th>
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
                <span className={`tag ${k.scope}`}>{k.scope}</span>
              </td>
              <td>{k.group_restriction || <span className="muted">—</span>}</td>
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
              <td colSpan={6} className="muted">
                No keys yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
