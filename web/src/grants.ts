import type { Capability, Grant, GrantKind, Site } from "./api";

// CAP_INFO describes each capability for the grant editor.
export const CAP_INFO: Record<Capability, { desc: string }> = {
  publish: { desc: "Create or replace sites (deploy)." },
  unpublish: { desc: "Soft-delete sites — takes them offline into quarantine." },
  rollback: { desc: "Restore a site’s previous version." },
  patch: { desc: "Edit site metadata — title, tags, visibility." },
  purge: { desc: "Permanently destroy a quarantined (unpublished) site." },
  read: { desc: "Read site files over MCP (list, read, search). Not used by the ingest API." },
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

// DraftGrant is one grant card's in-progress state in the grant editor.
export interface DraftGrant {
  id: number;
  type: ScopeType;
  target: string;
  permissions: Capability[];
  expanded: boolean;
}

let nextGrantId = 1;

// newDraftGrant returns an empty, expanded group grant for the editor.
export function newDraftGrant(init: Partial<DraftGrant> = {}): DraftGrant {
  return { id: nextGrantId++, type: "group", target: "", permissions: [], expanded: true, ...init };
}

// validateGrants checks a grant list the way the server will: at least one
// grant, a target on every group/site grant, at least one permission on every
// grant, and the risk acknowledgement when any grant is global. emptyMsg is the
// message for an empty list, which differs per screen.
export function validateGrants(grants: DraftGrant[], ack: boolean, emptyMsg: string): string | null {
  if (grants.length === 0) return emptyMsg;
  for (const g of grants) {
    if (g.type !== "global" && !g.target.trim()) return "Each group/site grant needs a target.";
    if (g.permissions.length === 0) return "Each grant needs at least one permission.";
  }
  if (grants.some((g) => g.type === "global") && !ack) {
    return "Acknowledge the global-scope risk to continue.";
  }
  return null;
}

// deriveTargets splits the site list into the set of existing site dirs and the
// set of group prefixes that contain them, for the grant editor's suggestions.
export function deriveTargets(sites: Site[]): { groups: string[]; sites: string[] } {
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
