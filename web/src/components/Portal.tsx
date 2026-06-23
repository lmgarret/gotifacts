import { useCallback, useEffect, useMemo, useState } from "react";
import { api, type Me, type Site, type TreeNode } from "../api";
import { GroupSection } from "./GroupSection";
import { SiteDetail } from "./SiteDetail";
import { SiteCreateModal } from "./SiteCreateModal";

interface Props {
  me: Me;
}

export function Portal({ me }: Props) {
  const [tree, setTree] = useState<TreeNode | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [count, setCount] = useState(0);
  const [allTags, setAllTags] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [q, setQ] = useState("");
  const [tag, setTag] = useState("");
  const [sort, setSort] = useState("date");
  const [showHidden, setShowHidden] = useState(false);
  const [selected, setSelected] = useState<Site | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(() => {
    api
      .listSites({ q, tag, sort, hidden: me.is_admin && showHidden })
      .then((res) => {
        setTree(res.tree);
        setSites(res.sites ?? []);
        setCount(res.count);
        const tags = new Set<string>();
        (res.sites ?? []).forEach((s) => s.tags?.forEach((t) => tags.add(t)));
        setAllTags([...tags].sort());
        setError(null);
      })
      .catch((e: Error) => setError(e.message));
  }, [q, tag, sort, showHidden, me.is_admin]);

  const groups = useMemo(
    () => [...new Set(sites.map((s) => s.group).filter(Boolean))].sort(),
    [sites],
  );

  useEffect(() => {
    const id = setTimeout(load, 150);
    return () => clearTimeout(id);
  }, [load]);

  const isEmpty = useMemo(
    () => !tree || (tree.groups.length === 0 && tree.sites.length === 0),
    [tree],
  );

  // Sandboxed preview iframes can steal focus when their embedded page loads
  // (e.g. a script calling focus()), making the browser scroll that iframe into
  // view and jumping the page. Guard centrally: remember the user's scroll
  // position and, if focus lands on a preview iframe, blur it and restore the
  // scroll. The correction runs synchronously in the same turn as the steal —
  // before paint — so there is no visible jump.
  useEffect(() => {
    let lastX = window.scrollX;
    let lastY = window.scrollY;
    const onScroll = () => {
      lastX = window.scrollX;
      lastY = window.scrollY;
    };
    const onFocusIn = (e: FocusEvent) => {
      const target = e.target as HTMLElement | null;
      if (target?.tagName === "IFRAME" && target.closest(".thumb")) {
        (target as HTMLIFrameElement).blur();
        window.scrollTo(lastX, lastY);
      }
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    document.addEventListener("focusin", onFocusIn, true);
    return () => {
      window.removeEventListener("scroll", onScroll);
      document.removeEventListener("focusin", onFocusIn, true);
    };
  }, []);

  return (
    <div className="portal">
      <div className="toolbar">
        <input
          type="search"
          placeholder="Search sites…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Search sites"
        />
        <select value={tag} onChange={(e) => setTag(e.target.value)} aria-label="Filter by tag">
          <option value="">All tags</option>
          {allTags.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <select value={sort} onChange={(e) => setSort(e.target.value)} aria-label="Sort">
          <option value="date">Newest</option>
          <option value="title">Title</option>
          <option value="slug">Slug</option>
        </select>
        {me.is_admin && (
          <label className="checkbox">
            <input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} />
            Show hidden
          </label>
        )}
        <span className="count">{count} site{count === 1 ? "" : "s"}</span>
        {me.is_admin && (
          <button className="add-site" onClick={() => setCreating(true)}>
            + Add site
          </button>
        )}
      </div>

      {error && <p className="error">{error}</p>}
      {isEmpty && !error && <p className="muted empty">No sites published yet.</p>}

      {tree && (
        <div className="tree">
          <GroupSection
            node={tree}
            base={me.base_domain}
            depth={0}
            onSelect={setSelected}
          />
        </div>
      )}

      {selected && (
        <SiteDetail
          site={selected}
          base={me.base_domain}
          isAdmin={me.is_admin}
          onClose={() => setSelected(null)}
          onChanged={() => {
            setSelected(null);
            load();
          }}
        />
      )}

      {creating && (
        <SiteCreateModal
          base={me.base_domain}
          existing={sites.map((s) => ({ group: s.group, slug: s.slug }))}
          groups={groups}
          onClose={() => setCreating(false)}
          onCreated={load}
        />
      )}
    </div>
  );
}
