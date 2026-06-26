import { useCallback, useEffect, useState } from "react";
import { api, type FileNode, type Revision, type Site } from "../api";
import { formatSize } from "../format";
import { FileTree } from "./FileTree";

interface Props {
  site: Site;
  isAdmin: boolean;
  // versioningEnabled reflects the server config. When false, only the live
  // ("current") revision ever exists: replacing a site overwrites it in place
  // and no prior versions are retained, so rollback is unavailable.
  versioningEnabled: boolean;
  // onRolledBack is called after a successful rollback so the parent can refresh.
  onRolledBack: (updated: Site) => void;
}

// FilesTab lets the user pick a revision, browse its files, download individual
// files or the whole revision as a zip, and (admins) roll back to it.
export function FilesTab({ site, isAdmin, versioningEnabled, onRolledBack }: Props) {
  const [revisions, setRevisions] = useState<Revision[]>([]);
  const [rev, setRev] = useState<string>("");
  const [tree, setTree] = useState<FileNode | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);

  // loadFiles fetches the file tree for a revision. Called from event handlers
  // (selection change) and after the initial revision list resolves — not from
  // an effect body — so loading state is set outside React's effect phase.
  const loadFiles = useCallback(
    (r: string) => {
      setLoading(true);
      setError(null);
      api
        .getRevisionFiles(site.group, site.slug, r)
        .then(setTree)
        .catch((e: Error) => setError(e.message))
        .finally(() => setLoading(false));
    },
    [site.group, site.slug],
  );

  // Load the revision list once (and whenever the site identity changes), then
  // load the files for the default (current) revision.
  useEffect(() => {
    let active = true;
    api
      .listRevisions(site.group, site.slug)
      .then((res) => {
        if (!active) return;
        const revs = res.revisions ?? [];
        setRevisions(revs);
        setError(null);
        const first = revs[0]?.id ?? "";
        setRev(first);
        if (first) loadFiles(first);
      })
      .catch((e: Error) => active && setError(e.message));
    return () => {
      active = false;
    };
  }, [site.group, site.slug, loadFiles]);

  const onSelectRev = (r: string) => {
    setRev(r);
    if (r) loadFiles(r);
  };

  const rollback = useCallback(() => {
    if (!rev || rev === "current") return;
    if (!confirm(`Roll back ${site.slug} to revision ${rev}? This becomes the live version.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    api
      .rollbackSite(site.group, site.slug, rev)
      .then((updated) => onRolledBack(updated))
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false));
  }, [rev, site.group, site.slug, onRolledBack]);

  const fmt = (iso: string) => {
    const d = new Date(iso);
    return isNaN(d.getTime()) ? iso : d.toLocaleString();
  };

  return (
    <div className="files-tab">
      <div className="files-toolbar">
        <label className="field-label" htmlFor="rev-select">
          Revision
        </label>
        <select
          id="rev-select"
          value={rev}
          onChange={(e) => onSelectRev(e.target.value)}
          disabled={revisions.length === 0}
        >
          {revisions.map((r) => (
            <option key={r.id} value={r.id}>
              {r.current ? "Current" : "Archived"} — {fmt(r.created_at)} — {formatSize(r.size)}
              {r.current ? "" : ` (${r.id})`}
            </option>
          ))}
        </select>
        {rev && (
          <a className="button" href={api.revisionArchiveURL(site.group, site.slug, rev)} download>
            Download all (.zip)
          </a>
        )}
        {isAdmin && rev && rev !== "current" && (
          <button disabled={busy} onClick={rollback}>
            Roll back to this revision
          </button>
        )}
      </div>

      {!versioningEnabled && (
        <p className="notice">
          Versioning is disabled.{" "}
          <a
            href="https://lmgarret.github.io/gotifacts/guides/versioning-and-rollback/"
            target="_blank"
            rel="noopener noreferrer"
          >
            Learn more
          </a>
        </p>
      )}

      {error && <p className="error">{error}</p>}
      {revisions.length === 0 && !error && (
        <p className="muted empty">No revisions available for this site.</p>
      )}
      {loading && <p className="muted">Loading files…</p>}
      {!loading && tree && (
        <FileTree
          node={tree}
          depth={0}
          downloadURL={(path) => api.revisionFileURL(site.group, site.slug, rev, path)}
        />
      )}
    </div>
  );
}
