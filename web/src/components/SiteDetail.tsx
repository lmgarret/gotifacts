import { useState } from "react";
import { api, type Site } from "../api";
import { siteURL } from "../sitehost";

interface Props {
  site: Site;
  base: string;
  isAdmin: boolean;
  onClose: () => void;
  onChanged: () => void;
}

export function SiteDetail({ site, base, isAdmin, onClose, onChanged }: Props) {
  const url = siteURL(site, base);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <button className="close" onClick={onClose} aria-label="Close">
          ×
        </button>
        <h2>{site.title || site.slug}</h2>
        <p className="muted">
          <a href={url} target="_blank" rel="noopener noreferrer">
            {url.replace("https://", "")}
          </a>
        </p>
        {site.description && <p>{site.description}</p>}
        <dl className="details">
          <dt>Group</dt>
          <dd>{site.group || <span className="muted">(flat)</span>}</dd>
          <dt>Slug</dt>
          <dd>{site.slug}</dd>
          {site.date && (
            <>
              <dt>Date</dt>
              <dd>{site.date}</dd>
            </>
          )}
          {site.repo && (
            <>
              <dt>Repo</dt>
              <dd>
                <a href={site.repo} target="_blank" rel="noopener noreferrer">
                  {site.repo}
                </a>
              </dd>
            </>
          )}
          <dt>Updated</dt>
          <dd>{new Date(site.updated_at).toLocaleString()}</dd>
        </dl>
        {site.tags?.length > 0 && (
          <div className="meta">
            {site.tags.map((t) => (
              <span key={t} className="tag">
                {t}
              </span>
            ))}
          </div>
        )}

        {err && <p className="error">{err}</p>}

        {isAdmin && (
          <div className="actions">
            <button
              disabled={busy}
              onClick={() =>
                run(() => api.patchSite(site.group, site.slug, { hidden: !site.hidden }))
              }
            >
              {site.hidden ? "Unhide" : "Hide"}
            </button>
            <button disabled={busy} onClick={() => run(() => api.rollbackSite(site.group, site.slug))}>
              Roll back
            </button>
            <button
              className="danger"
              disabled={busy}
              onClick={() => {
                if (confirm(`Delete ${site.slug}? This removes its files.`)) {
                  run(() => api.deleteSite(site.group, site.slug));
                }
              }}
            >
              Delete
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
