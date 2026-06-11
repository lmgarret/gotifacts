import { CAPABILITIES, type Capability } from "../api";
import { CAP_INFO, scopeImpact, type ScopeType } from "../grants";
import { TargetSelect } from "./TargetSelect";

export interface DraftGrant {
  id: number;
  type: ScopeType;
  target: string;
  permissions: Capability[];
  expanded: boolean;
}

interface Props {
  grant: DraftGrant;
  index: number;
  groups: string[];
  sites: string[];
  base: string;
  removable: boolean;
  onChange: (patch: Partial<DraftGrant>) => void;
  onRemove: () => void;
}

function scopeBadge(type: ScopeType) {
  return <span className={`target-badge ${type}`}>{type}</span>;
}

function scopeSummary(g: DraftGrant): string {
  if (g.type === "global") return "All sites";
  return g.target || "(choose a target)";
}

export function GrantCard({ grant: g, index, groups, sites, base, removable, onChange, onRemove }: Props) {
  const caps = g.permissions.length ? g.permissions.join(", ") : "no permissions yet";

  const toggleCap = (cap: Capability) =>
    onChange({
      permissions: g.permissions.includes(cap)
        ? g.permissions.filter((c) => c !== cap)
        : [...g.permissions, cap],
    });

  const setType = (type: ScopeType) =>
    onChange({ type, target: type === "global" ? "" : g.target });

  return (
    <div className={`grant-card ${g.type === "global" ? "is-global" : ""}`}>
      <button
        type="button"
        className="grant-head"
        onClick={() => onChange({ expanded: !g.expanded })}
        aria-expanded={g.expanded}
      >
        <span className="grant-chevron">{g.expanded ? "▾" : "▸"}</span>
        {scopeBadge(g.type)}
        <code className="grant-target">{scopeSummary(g)}</code>
        <span className="grant-caps muted">{caps}</span>
      </button>

      {removable && (
        <button type="button" className="grant-remove small" title="Remove grant" onClick={onRemove}>
          ✕
        </button>
      )}

      {g.expanded && (
        <div className="grant-body">
          <label className="field">
            <span className="field-label">Scope</span>
            <div className="scope-row">
              <select
                className="scope-type"
                value={g.type}
                onChange={(e) => setType(e.target.value as ScopeType)}
                aria-label={`Grant ${index + 1} scope type`}
              >
                <option value="group">Group (a subtree)</option>
                <option value="site">Site (one exact site)</option>
                <option value="global">Global (all sites)</option>
              </select>
              {g.type !== "global" && (
                <TargetSelect
                  kind={g.type}
                  value={g.target}
                  suggestions={g.type === "site" ? sites : groups}
                  base={base}
                  onChange={(target) => onChange({ target })}
                />
              )}
            </div>
            <span className={`scope-impact ${g.type === "global" ? "danger-text" : "muted"}`}>
              {scopeImpact(g.type, g.target, base)}
            </span>
          </label>

          <fieldset className="perm-set">
            <legend>Permissions</legend>
            {CAPABILITIES.map((cap) => (
              <label className="perm-row" key={cap}>
                <input
                  type="checkbox"
                  checked={g.permissions.includes(cap)}
                  onChange={() => toggleCap(cap)}
                />
                <span className="perm-name">{cap}</span>
                <span className="perm-desc muted">{CAP_INFO[cap].desc}</span>
              </label>
            ))}
          </fieldset>
        </div>
      )}
    </div>
  );
}
