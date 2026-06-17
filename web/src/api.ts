// API client for the gotifacts management plane. The reverse proxy injects the
// forward-auth identity header automatically; no credentials are handled here.

export interface Me {
  user: string;
  is_admin: boolean;
  base_domain: string;
  mcp_enabled: boolean;
}

export interface Site {
  id: number;
  group: string;
  slug: string;
  title: string;
  description: string;
  date?: string;
  tags: string[];
  repo?: string;
  preview?: string;
  hidden: boolean;
  created_at: string;
  updated_at: string;
}

export interface TreeNode {
  name: string;
  path: string;
  groups: TreeNode[];
  sites: Site[];
}

export interface SitesResponse {
  sites: Site[];
  tree: TreeNode;
  count: number;
}

export type Capability = "publish" | "unpublish" | "rollback" | "patch";

export const CAPABILITIES: Capability[] = ["publish", "unpublish", "rollback", "patch"];

export type GrantKind = "group" | "site";

export interface Grant {
  kind: GrantKind;
  // Group subtree or exact site path. Empty (group kind) means "all sites".
  target: string;
  permissions: Capability[];
}

export interface ApiKey {
  id: number;
  name: string;
  admin: boolean;
  grants: Grant[];
  created_at: string;
  last_used_at?: string;
  // RFC3339 instant; absent means the key never expires.
  expires_at?: string;
}

export interface CreatedKey extends ApiKey {
  key: string;
}

export interface CreateKeyBody {
  name: string;
  admin: boolean;
  grants: Grant[];
  // RFC3339 or YYYY-MM-DD; omit/empty for no expiration.
  expires_at?: string;
}

// Connection is one MCP connector authorization (an OAuth consent), aggregating
// all tokens it issued. Revoking it deletes those tokens.
export interface Connection {
  id: string;
  client_id: string;
  client_name: string;
  user: string;
  grants: Grant[];
  created_at: string;
  last_used_at?: string;
  expires_at: string;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // ignore non-JSON error bodies
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export interface ListParams {
  q?: string;
  tag?: string;
  group?: string;
  sort?: string;
  hidden?: boolean;
}

// PublishResult mirrors ingest.Result returned by POST /api/sites.
export interface PublishResult {
  url: string;
  group: string;
  slug: string;
  updated_at: string;
}

export interface PublishMeta {
  group?: string;
  slug: string;
  title?: string;
  description?: string;
  tags?: string[];
  repo?: string;
  preview?: string;
  hidden?: boolean;
}

// publishSite uploads a site via the admin multipart endpoint. A single .html
// file is sent as the "index" part; a .zip/.tar.gz archive as the "bundle" part
// (the server distinguishes them by magic bytes).
async function publishSite(meta: PublishMeta, file: File): Promise<PublishResult> {
  const form = new FormData();
  form.append("meta", JSON.stringify(meta));
  form.append(isArchive(file) ? "bundle" : "index", file, file.name);
  const res = await fetch("/api/sites", { method: "POST", body: form });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // ignore non-JSON error bodies
    }
    throw new Error(msg);
  }
  return res.json() as Promise<PublishResult>;
}

// isArchive reports whether a file should be sent as a bundle (zip/tar.gz)
// rather than a single index.html.
export function isArchive(file: File): boolean {
  const n = file.name.toLowerCase();
  return n.endsWith(".zip") || n.endsWith(".tar.gz") || n.endsWith(".tgz");
}

export const api = {
  me: () => request<Me>("/api/me"),

  listSites: (p: ListParams = {}) => {
    const qs = new URLSearchParams();
    if (p.q) qs.set("q", p.q);
    if (p.tag) qs.set("tag", p.tag);
    if (p.group) qs.set("group", p.group);
    if (p.sort) qs.set("sort", p.sort);
    if (p.hidden) qs.set("hidden", "true");
    const suffix = qs.toString() ? `?${qs}` : "";
    return request<SitesResponse>(`/api/sites${suffix}`);
  },

  patchSite: (group: string, slug: string, body: Partial<Site>) =>
    request<Site>(`/api/sites/${sitePath(group, slug)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),

  deleteSite: (group: string, slug: string) =>
    request<void>(`/api/sites/${sitePath(group, slug)}`, { method: "DELETE" }),

  rollbackSite: (group: string, slug: string) =>
    request<Site>(`/api/sites/${sitePath(group, slug)}/rollback`, { method: "POST" }),

  publishSite,

  listKeys: () => request<{ keys: ApiKey[] }>("/api/keys"),

  createKey: (body: CreateKeyBody) =>
    request<CreatedKey>("/api/keys", {
      method: "POST",
      body: JSON.stringify(body),
    }),

  deleteKey: (id: number) => request<void>(`/api/keys/${id}`, { method: "DELETE" }),

  listConnections: () => request<{ connections: Connection[] }>("/api/mcp/connections"),

  revokeConnection: (id: string) =>
    request<void>(`/api/mcp/connections/${encodeURIComponent(id)}`, { method: "DELETE" }),
};

function sitePath(group: string, slug: string): string {
  return group ? `${group}/${slug}` : slug;
}
