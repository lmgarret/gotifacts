import { useState, type FormEvent } from "react";
import { api, type Site } from "../api";

interface Props {
  site: Site;
  onClose: () => void;
  // onSaved receives the updated site returned by the PATCH response.
  onSaved: (updated: Site) => void;
}

// SiteEditModal edits a site's metadata in place via PATCH. It mirrors
// SiteCreateModal's markup and flow, but is pre-populated from the current
// site and never touches the deployed files (group/slug are identity).
export function SiteEditModal({ site, onClose, onSaved }: Props) {
  const [title, setTitle] = useState(site.title);
  const [description, setDescription] = useState(site.description);
  const [date, setDate] = useState(site.date ?? "");
  const [repo, setRepo] = useState(site.repo ?? "");
  const [tags, setTags] = useState(site.tags?.join(", ") ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const updated = await api.patchSite(site.group, site.slug, {
        title: title.trim(),
        description: description.trim(),
        date: date.trim(),
        repo: repo.trim(),
        tags: tags
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean),
      });
      onSaved(updated);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal sitemodal" onClick={(e) => e.stopPropagation()}>
        <button className="close" onClick={onClose} aria-label="Close">
          ×
        </button>

        <form onSubmit={submit}>
          <h2>
            Edit {site.group ? `${site.group}/` : ""}
            {site.slug}
          </h2>

          <label className="field">
            <span className="field-label">
              Title <span className="field-hint muted">(optional)</span>
            </span>
            <input value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>

          <label className="field">
            <span className="field-label">
              Description <span className="field-hint muted">(optional)</span>
            </span>
            <input value={description} onChange={(e) => setDescription(e.target.value)} />
          </label>

          <div className="field-row">
            <label className="field">
              <span className="field-label">
                Date <span className="field-hint muted">(optional)</span>
              </span>
              <input
                placeholder="e.g. 2026-06-25"
                value={date}
                onChange={(e) => setDate(e.target.value)}
              />
            </label>

            <label className="field">
              <span className="field-label">
                Repo <span className="field-hint muted">(optional)</span>
              </span>
              <input
                placeholder="https://github.com/…"
                value={repo}
                onChange={(e) => setRepo(e.target.value)}
              />
            </label>
          </div>

          <label className="field">
            <span className="field-label">
              Tags <span className="field-hint muted">(comma-separated, optional)</span>
            </span>
            <input value={tags} onChange={(e) => setTags(e.target.value)} />
          </label>

          {error && <p className="error">{error}</p>}

          <div className="actions">
            <button type="submit" disabled={busy}>
              {busy ? "Saving…" : "Save"}
            </button>
            <button type="button" className="ghost" onClick={onClose}>
              Cancel
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
