import { useEffect, useRef, useState } from "react";
import type { Me } from "../api";
import type { View } from "../router";

interface Item {
  view: View;
  label: string;
  badge?: number;
}

interface Props {
  me: Me;
  view: View;
  trashCount: number;
  onNavigate: (v: View) => void;
}

// AccountMenu replaces the old row of top-bar tabs. The brand logo is the home
// affordance (→ Portal); the admin-only management views live behind the user
// identity chip on the right, where account/settings menus are conventionally
// found. A non-admin only ever has the Portal view, so they get a plain chip
// with no menu.
export function AccountMenu({ me, view, trashCount, onNavigate }: Props) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  // Built from the same visibility rules the previous <nav> used.
  const items: Item[] = [{ view: "portal", label: "Portal" }];
  if (me.is_admin) {
    items.push({ view: "keys", label: "API Keys" });
    if (me.mcp_enabled) items.push({ view: "connections", label: "Connections" });
    items.push({ view: "trash", label: "Trash", badge: trashCount });
  }

  const hasMenu = items.length > 1;

  // Close on outside click or Escape while open.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!hasMenu) {
    return (
      <div className="who">
        {me.user}
        {me.is_admin && <span className="badge">admin</span>}
      </div>
    );
  }

  const go = (v: View) => {
    onNavigate(v);
    setOpen(false);
  };

  return (
    <div className="account" ref={wrapRef}>
      <button
        type="button"
        className={`account-trigger${open ? " open" : ""}`}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <span className="account-user">{me.user}</span>
        {me.is_admin && <span className="badge">admin</span>}
        {/* Hints there is something in Trash without opening the menu. */}
        {trashCount > 0 && !open && <span className="account-dot" aria-hidden="true" />}
        <span className="account-caret" aria-hidden="true">
          ▾
        </span>
      </button>
      {open && (
        <ul className="account-menu" role="menu">
          {items.map((it) => (
            <li key={it.view} role="none">
              <button
                type="button"
                role="menuitem"
                className={`account-item${view === it.view ? " active" : ""}`}
                onClick={() => go(it.view)}
              >
                <span>{it.label}</span>
                {it.badge ? <span className="nav-badge">{it.badge}</span> : null}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
