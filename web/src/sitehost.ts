import type { Site } from "./api";

// siteHost mirrors the server's URL⇄path convention: the host is
// slug.<group-segments-reversed>.<base>.
export function siteHost(site: Pick<Site, "group" | "slug">, base: string): string {
  const labels = [site.slug];
  if (site.group) {
    const segs = site.group.split("/").filter(Boolean);
    for (let i = segs.length - 1; i >= 0; i--) labels.push(segs[i]);
  }
  labels.push(base);
  return labels.join(".");
}

export function siteURL(site: Pick<Site, "group" | "slug">, base: string): string {
  return `https://${siteHost(site, base)}`;
}
