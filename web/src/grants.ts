import type { Capability, Grant, GrantKind } from "./api";

// CAP_INFO describes each capability for the grant editor.
export const CAP_INFO: Record<Capability, { desc: string }> = {
  publish: { desc: "Create or replace sites (deploy)." },
  unpublish: { desc: "Delete sites." },
  rollback: { desc: "Restore a site’s previous version." },
  patch: { desc: "Edit site metadata — title, tags, visibility." },
};

// ScopeType is the UI-level grant scope. "global" maps to a group grant with an
// empty target on the wire; "group"/"site" map to the matching kind.
export type ScopeType = "group" | "site" | "global";

// LABEL_RE mirrors the server's site-path label rule.
export const LABEL_RE = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;

// validPath reports whether dir is a syntactically valid 1–3 label path.
export function validPath(dir: string): boolean {
  const segs = dir.split("/").filter(Boolean);
  return segs.length >= 1 && segs.length <= 3 && segs.every((s) => LABEL_RE.test(s));
}

// hostForDir reverses a site/group directory into its subdomain, matching the
// server's URL⇄path convention (group segments reversed, slug leftmost).
export function hostForDir(dir: string, base: string): string {
  const segs = dir.split("/").filter(Boolean);
  return [...segs].reverse().concat(base || "").filter(Boolean).join(".");
}

// scopeImpact describes, in plain language, the subdomains a scope affects.
export function scopeImpact(type: ScopeType, target: string, base: string): string {
  const b = base || "your base domain";
  if (type === "global") return `Affects ALL sites under ${b}.`;
  const host = hostForDir(target, base) || b;
  if (type === "site") return `Affects only ${host}.`;
  return `Affects ${host} and everything under *.${host}.`;
}

// toGrant converts an editor scope + permissions into the wire Grant.
export function toGrant(type: ScopeType, target: string, permissions: Capability[]): Grant {
  const kind: GrantKind = type === "site" ? "site" : "group";
  return { kind, target: type === "global" ? "" : target.trim(), permissions };
}

// scopeTypeOf derives the UI scope type from a stored grant.
export function scopeTypeOf(g: Grant): ScopeType {
  if (g.kind === "site") return "site";
  return g.target === "" ? "global" : "group";
}
