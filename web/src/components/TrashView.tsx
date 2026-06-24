import { useCallback, useEffect, useState } from "react";
import { api, type Site } from "../api";

interface Props {
  onCountChange?: (n: number) => void;
}

function fmtDeleted(deletedAt: string): string {
  const d = new Date(deletedAt);
  const diffMs = Date.now() - d.getTime();
  const days = Math.floor(diffMs / (1000 * 60 * 60 * 24));
  if (days === 0) return "today";
  if (days === 1) return "1 day ago";
  return `${days} days ago`;
}

export function TrashView({ onCountChange }: Props) {
  const [sites, setSites] = useState<Site[]>([]);
  const [busy, setBusy] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .listDeletedSites()
      .then((res) => {
        setSites(res.sites ?? []);
        onCountChange?.(res.count);
        setError(null);
      })
      .catch((e: Error) => setError(e.message));
  }, [onCountChange]);

  useEffect(() => {
    load();
  }, [load]);

  const run = async (id: number, fn: () => Promise<unknown>) => {
    setBusy(id);
    setError(null);
    try {
      await fn();
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="trash">
      <div className="trash-head">
        <h2>Trash</h2>
        <p className="muted">
          Unpublished sites are held in quarantine until the server's retention period
          expires or you purge them manually. Restore brings a site back online
          immediately.
        </p>
      </div>

      {error && <p className="error">{error}</p>}

      {sites.length === 0 && !error && (
        <p className="muted empty">No deleted sites in quarantine.</p>
      )}

      {sites.length > 0 && (
        <div className="table-wrap">
          <table className="sites-table">
            <thead>
              <tr>
                <th className="col-title">Site</th>
                <th className="col-group">Group</th>
                <th className="col-date">Deleted</th>
                <th className="col-actions"></th>
              </tr>
            </thead>
            <tbody>
              {sites.map((site) => (
                <tr key={site.id}>
                  <td className="col-title">
                    <span className="t-title">{site.title || site.slug}</span>
                    {site.title && <span className="t-desc">{site.slug}</span>}
                  </td>
                  <td className="col-group">{site.group || "—"}</td>
                  <td className="col-date">
                    {site.deleted_at ? fmtDeleted(site.deleted_at) : "—"}
                  </td>
                  <td className="col-actions">
                    <div className="row-actions">
                      <button
                        className="small"
                        disabled={busy === site.id}
                        onClick={() => run(site.id, () => api.restoreSite(site.group, site.slug))}
                      >
                        Restore
                      </button>
                      <button
                        className="small danger"
                        disabled={busy === site.id}
                        onClick={() => {
                          if (confirm(`Permanently delete ${site.slug}? This cannot be undone.`)) {
                            run(site.id, () => api.purgeSite(site.group, site.slug));
                          }
                        }}
                      >
                        Delete permanently
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
