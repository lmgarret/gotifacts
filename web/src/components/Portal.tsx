import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, type Me, type Site, type TreeNode } from "../api";
import { GroupSection } from "./GroupSection";
import { SitesTable } from "./SitesTable";
import { SiteCreateModal } from "./SiteCreateModal";
import { useShakeGravity } from "../useShakeGravity";

interface Props {
  me: Me;
  // onOpenSite navigates to the dedicated page for the chosen site.
  onOpenSite: (s: Site) => void;
}

type Layout = "card" | "table";

const LAYOUT_KEY = "gotifacts.layout";

function initialLayout(): Layout {
  return localStorage.getItem(LAYOUT_KEY) === "table" ? "table" : "card";
}

export function Portal({ me, onOpenSite }: Props) {
  const [tree, setTree] = useState<TreeNode | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [count, setCount] = useState(0);
  const [allTags, setAllTags] = useState<string[]>([]);
  const [allGroups, setAllGroups] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [q, setQ] = useState("");
  const [tag, setTag] = useState("");
  const [group, setGroup] = useState("");
  const [sort, setSort] = useState("date");
  const [showHidden, setShowHidden] = useState(false);
  const [creating, setCreating] = useState(false);
  const [layout, setLayout] = useState<Layout>(initialLayout);

  // Easter egg: shake the phone (or enter the Konami code) to drop the visible
  // cards under gravity. Scoped to this container, which holds the card grid.
  const portalRef = useRef<HTMLDivElement>(null);
  const gravity = useShakeGravity(portalRef);

  useEffect(() => {
    localStorage.setItem(LAYOUT_KEY, layout);
  }, [layout]);

  const load = useCallback(() => {
    api
      .listSites({ q, tag, group, sort, hidden: me.is_admin && showHidden })
      .then((res) => {
        setTree(res.tree);
        setSites(res.sites ?? []);
        setCount(res.count);
        const tags = new Set<string>();
        (res.sites ?? []).forEach((s) => s.tags?.forEach((t) => tags.add(t)));
        setAllTags([...tags].sort());
        // Capture the full group list only when unfiltered, so selecting a
        // group does not collapse the dropdown to that subtree.
        if (!group) {
          const gs = new Set<string>();
          (res.sites ?? []).forEach((s) => s.group && gs.add(s.group));
          setAllGroups([...gs].sort());
        }
        setError(null);
      })
      .catch((e: Error) => setError(e.message));
  }, [q, tag, group, sort, showHidden, me.is_admin]);

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
    <div className="portal" ref={portalRef}>
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
        {allGroups.length > 0 && (
          <select
            value={group}
            onChange={(e) => setGroup(e.target.value)}
            aria-label="Filter by group"
          >
            <option value="">All groups</option>
            {allGroups.map((g) => (
              <option key={g} value={g}>
                {g}
              </option>
            ))}
          </select>
        )}
        {layout === "card" && (
          <select value={sort} onChange={(e) => setSort(e.target.value)} aria-label="Sort">
            <option value="date">Newest</option>
            <option value="title">Title</option>
            <option value="slug">Slug</option>
          </select>
        )}
        {me.is_admin && (
          <label className="checkbox">
            <input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} />
            Show hidden
          </label>
        )}
        <span className="count">{count} site{count === 1 ? "" : "s"}</span>
        <div className="layout-toggle" role="group" aria-label="Layout">
          <button
            className={layout === "card" ? "active" : ""}
            onClick={() => setLayout("card")}
            aria-pressed={layout === "card"}
            title="Card view"
          >
            ▦
          </button>
          <button
            className={layout === "table" ? "active" : ""}
            onClick={() => setLayout("table")}
            aria-pressed={layout === "table"}
            title="Table view"
          >
            ☰
          </button>
        </div>
        {me.is_admin && (
          <button className="add-site" onClick={() => setCreating(true)}>
            + Add site
          </button>
        )}
      </div>

      {error && <p className="error">{error}</p>}
      {isEmpty && !error && <p className="muted empty">No sites published yet.</p>}

      {tree && layout === "card" && (
        <div className="tree">
          <GroupSection node={tree} base={me.base_domain} depth={0} onSelect={onOpenSite} />
        </div>
      )}

      {layout === "table" && sites.length > 0 && (
        <SitesTable sites={sites} base={me.base_domain} onSelect={onOpenSite} />
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

      {gravity.active && (
        <button className="gravity-reset" onClick={gravity.deactivate}>
          ↺ put the cards back
        </button>
      )}
    </div>
  );
}
