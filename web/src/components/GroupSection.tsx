import { useState } from "react";
import type { Site, TreeNode } from "../api";
import { SiteCard } from "./SiteCard";

interface Props {
  node: TreeNode;
  base: string;
  depth: number;
  onSelect: (s: Site) => void;
}

// GroupSection renders a collapsible group with its sites and nested subgroups.
export function GroupSection({ node, base, depth, onSelect }: Props) {
  const [open, setOpen] = useState(true);
  const isRoot = depth === 0;

  return (
    <section className="group" data-depth={depth}>
      {!isRoot && (
        <button className="group-header" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          <span className="chevron">{open ? "▾" : "▸"}</span>
          <span className="group-name">{node.name}</span>
          <span className="group-path muted">{node.path}</span>
        </button>
      )}
      {open && (
        <div className="group-body">
          {node.sites.length > 0 && (
            <div className="cards">
              {node.sites.map((s) => (
                <SiteCard key={`${s.group}/${s.slug}`} site={s} base={base} onSelect={onSelect} />
              ))}
            </div>
          )}
          {node.groups.map((g) => (
            <GroupSection key={g.path} node={g} base={base} depth={depth + 1} onSelect={onSelect} />
          ))}
        </div>
      )}
    </section>
  );
}
