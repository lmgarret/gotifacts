import { useMemo, useState } from "react";
import type { Site } from "../api";
import { siteURL } from "../sitehost";
import { formatSize } from "../format";
import { Favicon } from "./Favicon";

interface Props {
  sites: Site[];
  base: string;
  onSelect: (s: Site) => void;
}

type SortKey = "title" | "slug" | "group" | "date" | "updated" | "size";
type Dir = "asc" | "desc";

// Default sort direction per column: text sorts ascending, dates and size
// descending (largest/newest first), matching what a reader usually wants on
// first click.
const DEFAULT_DIR: Record<SortKey, Dir> = {
  title: "asc",
  slug: "asc",
  group: "asc",
  date: "desc",
  updated: "desc",
  size: "desc",
};

function siteTitle(s: Site): string {
  return s.title || s.slug;
}

// compare returns a stable ordering for one column; callers apply direction.
function compare(a: Site, b: Site, key: SortKey): number {
  switch (key) {
    case "title":
      return siteTitle(a).localeCompare(siteTitle(b), undefined, { sensitivity: "base" });
    case "slug":
      return a.slug.localeCompare(b.slug, undefined, { sensitivity: "base" });
    case "group":
      return (a.group || "").localeCompare(b.group || "", undefined, { sensitivity: "base" });
    case "date":
      // The user-supplied date is optional; missing dates sort last.
      return (a.date || "").localeCompare(b.date || "");
    case "updated":
      return a.updated_at.localeCompare(b.updated_at);
    case "size":
      return (a.size ?? 0) - (b.size ?? 0);
  }
}

function fmtDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

// SitesTable renders all sites in one flat, column-sortable table. Grouping is
// represented by a sortable Group column rather than the collapsible tree.
export function SitesTable({ sites, base, onSelect }: Props) {
  const [sortKey, setSortKey] = useState<SortKey>("updated");
  const [dir, setDir] = useState<Dir>("desc");

  const sorted = useMemo(() => {
    const factor = dir === "asc" ? 1 : -1;
    return [...sites].sort((a, b) => {
      const primary = compare(a, b, sortKey) * factor;
      // Tie-break on title so equal keys keep a deterministic order.
      return primary !== 0 ? primary : compare(a, b, "title");
    });
  }, [sites, sortKey, dir]);

  const toggle = (key: SortKey) => {
    if (key === sortKey) {
      setDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setDir(DEFAULT_DIR[key]);
    }
  };

  const header = (key: SortKey, label: string) => {
    const active = key === sortKey;
    return (
      <th
        className={`sortable${active ? " active" : ""}`}
        aria-sort={active ? (dir === "asc" ? "ascending" : "descending") : "none"}
      >
        <button onClick={() => toggle(key)}>
          {label}
          <span className="sort-arrow" aria-hidden="true">
            {active ? (dir === "asc" ? "▲" : "▼") : ""}
          </span>
        </button>
      </th>
    );
  };

  return (
    <div className="table-wrap">
      <table className="sites-table">
        <thead>
          <tr>
            <th className="col-icon" aria-label="Favicon" />
            {header("title", "Title")}
            {header("group", "Group")}
            {header("slug", "Slug")}
            {header("date", "Date")}
            {header("updated", "Updated")}
            {header("size", "Size")}
            <th className="col-tags">Tags</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((s) => {
            const url = siteURL(s, base);
            const title = siteTitle(s);
            return (
              <tr
                key={`${s.group}/${s.slug}`}
                className={s.hidden ? "hidden" : undefined}
                onClick={() => onSelect(s)}
                role="button"
                tabIndex={0}
              >
                <td className="col-icon">
                  <Favicon url={url} title={title} />
                </td>
                <td className="col-title">
                  <span className="t-title">{title}</span>
                  {s.description && <span className="t-desc">{s.description}</span>}
                </td>
                <td className="col-group">{s.group || "—"}</td>
                <td className="col-slug">{s.slug}</td>
                <td className="col-date">{fmtDate(s.date)}</td>
                <td className="col-date">{fmtDate(s.updated_at)}</td>
                <td className="col-size">{formatSize(s.size)}</td>
                <td className="col-tags">
                  <div className="meta">
                    {s.tags?.map((t) => (
                      <span key={t} className="tag">
                        {t}
                      </span>
                    ))}
                    {s.hidden && <span className="tag warn">hidden</span>}
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
