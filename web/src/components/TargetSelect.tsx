import { useMemo, useRef, useState } from "react";
import type { GrantKind } from "../api";
import { hostForDir, validPath } from "../grants";

interface Option {
  value: string;
  exists: boolean;
}

interface Props {
  kind: GrantKind; // fixed by the parent (group | site); decides suggestions + styling
  value: string;
  suggestions: string[]; // existing groups or sites of this kind
  base: string;
  onChange: (target: string) => void;
}

// TargetSelect is a combobox for a group or site path: it suggests existing
// targets of the given kind, lets you type a future one, and previews the
// affected subdomain on hover. The kind (and thus visual identity) is fixed by
// the caller; "global" is offered separately at the grant level.
export function TargetSelect({ kind, value, suggestions, base, onChange }: Props) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState(value);
  const blurTimer = useRef<number | undefined>(undefined);

  if (!open && query !== value) setQuery(value);

  const options = useMemo<Option[]>(() => {
    const q = query.trim().toLowerCase().replace(/^\/+|\/+$/g, "");
    const out: Option[] = [];
    for (const s of suggestions) if (q === "" || s.includes(q)) out.push({ value: s, exists: true });
    if (q !== "" && validPath(q) && !suggestions.includes(q)) out.push({ value: q, exists: false });
    return out;
  }, [query, suggestions]);

  const commit = (v: string) => {
    onChange(v);
    setQuery(v);
    setOpen(false);
  };

  return (
    <div className="target-input-wrap">
      <input
        className={`target-input ${kind}`}
        placeholder={kind === "site" ? "group/slug — pick or type a site" : "pick or type a group"}
        value={query}
        title={value ? `${hostForDir(value, base)}` : undefined}
        onChange={(e) => {
          const v = e.target.value;
          setQuery(v);
          setOpen(true);
          onChange(v.trim().toLowerCase().replace(/^\/+|\/+$/g, ""));
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => {
          blurTimer.current = window.setTimeout(() => setOpen(false), 120);
        }}
      />
      {open && options.length > 0 && (
        <ul className="target-options" onMouseDown={() => window.clearTimeout(blurTimer.current)}>
          {options.map((o) => (
            <li
              key={o.value}
              className={`target-option ${kind} ${o.exists ? "exists" : "new"}`}
              title={hostForDir(o.value, base)}
              onClick={() => commit(o.value)}
            >
              <span className="opt-kind">{kind}</span>
              <span className="opt-target">{o.value}</span>
              <span className="opt-state">{o.exists ? "exists" : "new"}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
