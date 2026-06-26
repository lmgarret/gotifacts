import { useState } from "react";
import { api, type Site } from "../api";
import { siteURL } from "../sitehost";
import { formatSize } from "../format";
import { FilesTab } from "./FilesTab";
import { SiteEditModal } from "./SiteEditModal";

interface Props {
  site: Site;
  base: string;
  isAdmin: boolean;
  // versioningEnabled reflects the server config; when false, replacing a site
  // overwrites it in place and no prior revisions are retained.
  versioningEnabled: boolean;
  onBack: () => void;
  // onGone is called after the site is deleted, so the parent can navigate away.
  onGone: () => void;
}

type Tab = "overview" | "files";

// SitePage is the dedicated per-site view. It is a tabbed shell so additional
// site features can be added as new tabs over time.
export function SitePage({ site: initial, base, isAdmin, versioningEnabled, onBack, onGone }: Props) {
  const [site, setSite] = useState<Site>(initial);
  const [tab, setTab] = useState<Tab>("overview");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const url = siteURL(site, base);

  const toggleHidden = () => {
    setBusy(true);
    setErr(null);
    api
      .patchSite(site.group, site.slug, { hidden: !site.hidden })
      .then(setSite)
      .catch((e: Error) => setErr(e.message))
      .finally(() => setBusy(false));
  };

  const unpublish = () => {
    if (
      !confirm(
        `Unpublish ${site.slug}? The site goes offline immediately. Files are kept in quarantine and can be restored by re-publishing the same slug.`,
      )
    ) {
      return;
    }
    setBusy(true);
    setErr(null);
    api
      .deleteSite(site.group, site.slug)
      .then(onGone)
      .catch((e: Error) => {
        setErr(e.message);
        setBusy(false);
      });
  };

  return (
    <div className="sitepage">
      <div className="sitepage-head">
        <button className="ghost back" onClick={onBack}>
          ← Back
        </button>
        <h2>{site.title || site.slug}</h2>
        <p className="muted">
          <a href={url} target="_blank" rel="noopener noreferrer">
            {url.replace("https://", "")}
          </a>
        </p>
      </div>

      <div className="tabs" role="tablist">
        <button
          role="tab"
          className={`tab ${tab === "overview" ? "active" : ""}`}
          aria-selected={tab === "overview"}
          onClick={() => setTab("overview")}
        >
          Overview
        </button>
        <button
          role="tab"
          className={`tab ${tab === "files" ? "active" : ""}`}
          aria-selected={tab === "files"}
          onClick={() => setTab("files")}
        >
          Files
        </button>
      </div>

      {err && <p className="error">{err}</p>}

      {tab === "overview" && (
        <div className="tab-panel">
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
            <dt>Size</dt>
            <dd>{formatSize(site.size)}</dd>
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
          {isAdmin && (
            <div className="actions">
              <button disabled={busy} onClick={() => setEditing(true)}>
                Edit
              </button>
              <button disabled={busy} onClick={toggleHidden}>
                {site.hidden ? "Unhide" : "Hide"}
              </button>
              <button className="danger" disabled={busy} onClick={unpublish}>
                Unpublish
              </button>
            </div>
          )}
        </div>
      )}

      {tab === "files" && (
        <div className="tab-panel">
          <FilesTab
            site={site}
            isAdmin={isAdmin}
            versioningEnabled={versioningEnabled}
            onRolledBack={setSite}
          />
        </div>
      )}

      {editing && (
        <SiteEditModal
          site={site}
          onClose={() => setEditing(false)}
          onSaved={(updated) => {
            setSite(updated);
            setEditing(false);
          }}
        />
      )}
    </div>
  );
}
