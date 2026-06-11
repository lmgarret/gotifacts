import { useMemo, useRef, useState } from "react";
import type { GrantKind } from "../api";

// LABEL_RE mirrors the server's site-path label rule.
const LABEL_RE = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;

// hostForDir reverses a site/group directory into its subdomain, matching the
// server's URL⇄path convention (group segments reversed, slug leftmost).
export function hostForDir(dir: string, base: string): string {
  const segs = dir.split("/").filter(Boolean);
  return [...segs].reverse().concat(base).join(".");
}

// validPath reports whether dir is a syntactically valid 1–3 label site path.
function validPath(dir: string): boolean {
  const segs = dir.split("/").filter(Boolean);
  return segs.length >= 1 && segs.length <= 3 && segs.every((s) => LABEL_RE.test(s));
}

// impact describes the subdomains a (kind, target) grant affects, for tooltips.
export function impact(kind: GrantKind, target: string, base: string): string {
  if (kind === "group") {
    if (target === "") return `All sites under ${base}`;
    const h = hostForDir(target, base);
    return `${h} and everything under *.${h}`;
  }
  return `Only ${hostForDir(target, base)}`;
}

interface Option {
  kind: GrantKind;
  target: string;
  exists: boolean;
  global?: boolean;
}

interface Props {
  kind: GrantKind;
  target: string;
  groups: string[];
  sites: string[];
  base: string;
  onChange: (kind: GrantKind, target: string) => void;
}

export function TargetSelect({ kind, target, groups, sites, base, onChange }: Props) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState(target);
  const blurTimer = useRef<number | undefined>(undefined);

  // Keep the visible query in sync when the grant is changed externally.
  if (!open && query !== target) setQuery(target);

  const options = useMemo<Option[]>(() => {
    const q = query.trim().toLowerCase().replace(/^\/+|\/+$/g, "");
    const match = (v: string) => q === "" || v.includes(q);
    const out: Option[] = [];

    // Global (no restriction) — always offered at the top.
    if (match("all") || match("") || q === "*") {
      out.push({ kind: "group", target: "", exists: true, global: true });
    }
    for (const g of groups) if (match(g)) out.push({ kind: "group", target: g, exists: true });
    for (const s of sites) if (match(s)) out.push({ kind: "site", target: s, exists: true });

    // Offer creating a future group/site for a typed value that isn't known.
    if (q !== "" && validPath(q)) {
      if (!groups.includes(q)) out.push({ kind: "group", target: q, exists: false });
      if (!sites.includes(q)) out.push({ kind: "site", target: q, exists: false });
    }
    return out;
  }, [query, groups, sites]);

  const pick = (o: Option) => {
    onChange(o.kind, o.target);
    setQuery(o.target);
    setOpen(false);
  };

  return (
    <div className="target-select">
      <span className={`target-badge ${kind} ${target === "" && kind === "group" ? "global" : ""}`}>
        {target === "" && kind === "group" ? "global" : kind}
      </span>
      <div className="target-input-wrap">
        <input
          placeholder="group or site (type to search or add)"
          value={query}
          title={impact(kind, target, base)}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
            // Raw typing defaults to a group target; pick a site option to switch.
            onChange("group", e.target.value.trim().toLowerCase().replace(/^\/+|\/+$/g, ""));
          }}
          onFocus={() => setOpen(true)}
          onBlur={() => {
            blurTimer.current = window.setTimeout(() => setOpen(false), 120);
          }}
        />
        {open && options.length > 0 && (
          <ul
            className="target-options"
            onMouseDown={() => window.clearTimeout(blurTimer.current)}
          >
            {options.map((o, i) => (
              <li
                key={`${o.kind}:${o.target}:${i}`}
                className={`target-option ${o.kind} ${o.exists ? "exists" : "new"} ${o.global ? "global" : ""}`}
                title={impact(o.kind, o.target, base)}
                onClick={() => pick(o)}
              >
                <span className="opt-kind">{o.global ? "global" : o.kind}</span>
                <span className="opt-target">
                  {o.global ? "All sites" : o.target}
                </span>
                <span className="opt-state">{o.exists ? "exists" : "new"}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
