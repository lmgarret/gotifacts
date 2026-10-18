import { useRef, useState, type FormEvent } from "react";
import { api, type CreatedKey } from "../api";
import { newDraftGrant, toGrant, validateGrants, type DraftGrant } from "../grants";
import { GrantsEditor } from "./GrantsEditor";

interface Props {
  groups: string[];
  sites: string[];
  base: string;
  onClose: () => void;
  onCreated: () => void;
}

const TODAY = new Date().toISOString().slice(0, 10);

export function KeyCreateModal({ groups, sites, base, onClose, onCreated }: Props) {
  const [name, setName] = useState("");
  const [admin, setAdmin] = useState(false);
  const [grants, setGrants] = useState<DraftGrant[]>(() => [newDraftGrant()]);
  const [expires, setExpires] = useState(""); // YYYY-MM-DD; "" = never
  const [ack, setAck] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<CreatedKey | null>(null);

  const validate = (): string | null => {
    if (!name.trim()) return "Give the key a name.";
    if (admin) return null;
    return validateGrants(grants, ack, "Add at least one grant, or make this an admin key.");
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const v = validate();
    if (v) {
      setError(v);
      return;
    }
    setError(null);
    try {
      const k = await api.createKey({
        name: name.trim(),
        admin,
        grants: admin ? [] : grants.map((g) => toGrant(g.type, g.target, g.permissions)),
        expires_at: expires || undefined,
      });
      setCreated(k);
      onCreated();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal keymodal" onClick={(e) => e.stopPropagation()}>
        <button className="close" onClick={onClose} aria-label="Close">
          ×
        </button>

        {created ? (
          <KeyReveal created={created} onClose={onClose} />
        ) : (
          <form onSubmit={submit}>
            <h2>Create API key</h2>

            <label className="field">
              <span className="field-label">Key name</span>
              <input
                placeholder="e.g. ci-previews"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
              />
              <span className="field-hint muted">A label to recognize this key later.</span>
            </label>

            <label className="checkbox admin-toggle">
              <input type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} />
              <span>
                <strong>Admin key</strong> — full access: manage keys and every site. Skips grants.
              </span>
            </label>

            {!admin && (
              <GrantsEditor
                grants={grants}
                onGrantsChange={setGrants}
                groups={groups}
                sites={sites}
                base={base}
                ack={ack}
                onAckChange={setAck}
                subject="key"
              />
            )}

            <label className="field">
              <span className="field-label">
                Expiration <span className="field-hint muted">(optional)</span>
              </span>
              <input
                type="date"
                min={TODAY}
                value={expires}
                onChange={(e) => setExpires(e.target.value)}
              />
              <span className="field-hint muted">Leave blank to never expire.</span>
            </label>

            {error && <p className="error">{error}</p>}

            <div className="actions">
              <button type="submit">Create key</button>
              <button type="button" className="ghost" onClick={onClose}>
                Cancel
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}

// KeyReveal shows the freshly-minted token once, with a copy button.
function KeyReveal({ created, onClose }: { created: CreatedKey; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created.key);
    } catch {
      // Fallback for non-secure contexts without the Clipboard API.
      inputRef.current?.select();
      document.execCommand("copy");
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="key-reveal">
      <h2>Key created</h2>
      <p className="muted">
        Copy <strong>{created.name}</strong> now — for security the token is shown{" "}
        <strong>only once</strong> and cannot be retrieved later.
      </p>

      <div className="token-row">
        <input
          ref={inputRef}
          className="token-field"
          readOnly
          value={created.key}
          onFocus={(e) => e.currentTarget.select()}
          aria-label="API token"
        />
        <button type="button" className={`copy-btn ${copied ? "copied" : ""}`} onClick={copy}>
          {copied ? "Copied ✓" : "Copy"}
        </button>
      </div>

      <dl className="reveal-meta">
        <dt>Access</dt>
        <dd>{created.admin ? "admin (all sites)" : `${created.grants.length} grant(s)`}</dd>
        <dt>Expires</dt>
        <dd>{created.expires_at ? new Date(created.expires_at).toLocaleString() : "never"}</dd>
      </dl>

      <div className="actions">
        <button onClick={onClose}>Done</button>
      </div>
    </div>
  );
}
