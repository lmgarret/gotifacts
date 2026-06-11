// API client for the gotifacts management plane. The reverse proxy injects the
// forward-auth identity header automatically; no credentials are handled here.

export interface Me {
  user: string;
  is_admin: boolean;
  base_domain: string;
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

export interface Grant {
  group: string;
  permissions: Capability[];
}

export interface ApiKey {
  id: number;
  name: string;
  admin: boolean;
  grants: Grant[];
  created_at: string;
  last_used_at?: string;
}

export interface CreatedKey extends ApiKey {
  key: string;
}

export interface CreateKeyBody {
  name: string;
  admin: boolean;
  grants: Grant[];
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

  listKeys: () => request<{ keys: ApiKey[] }>("/api/keys"),

  createKey: (body: CreateKeyBody) =>
    request<CreatedKey>("/api/keys", {
      method: "POST",
      body: JSON.stringify(body),
    }),

  deleteKey: (id: number) => request<void>(`/api/keys/${id}`, { method: "DELETE" }),
};

function sitePath(group: string, slug: string): string {
  return group ? `${group}/${slug}` : slug;
}
