import { useMemo, useRef, useState, type DragEvent, type FormEvent } from "react";
import { api, isArchive, type PublishResult } from "../api";

interface Props {
  base: string;
  // Existing (group, slug) pairs, used to warn before overwriting a live site.
  existing: { group: string; slug: string }[];
  // Known group paths, offered as datalist suggestions.
  groups: string[];
  onClose: () => void;
  onCreated: () => void;
}

const LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
const ACCEPT = ".html,.htm,.zip,.tar.gz,.tgz";

// deriveSlug turns a dropped file name into a candidate slug.
function deriveSlug(filename: string): string {
  return filename
    .replace(/\.(html?|zip|tar\.gz|tgz)$/i, "")
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function validGroup(group: string): boolean {
  if (!group) return true;
  const segs = group.split("/").filter(Boolean);
  if (segs.length > 2) return false; // group segments + slug must be ≤ 3
  return segs.every((s) => LABEL.test(s));
}

export function SiteCreateModal({ base, existing, groups, onClose, onCreated }: Props) {
  const [file, setFile] = useState<File | null>(null);
  const [dragging, setDragging] = useState(false);
  const [group, setGroup] = useState("");
  const [slug, setSlug] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState("");
  const [hidden, setHidden] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<PublishResult | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const normGroup = group.trim().toLowerCase().replace(/^\/+|\/+$/g, "");
  const exists = useMemo(
    () => existing.some((s) => s.group === normGroup && s.slug === slug.trim().toLowerCase()),
    [existing, normGroup, slug],
  );

  const pick = (f: File | null) => {
    if (!f) return;
    setFile(f);
    setError(null);
    if (!slug.trim()) setSlug(deriveSlug(f.name));
  };

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    pick(e.dataTransfer.files?.[0] ?? null);
  };

  const validate = (): string | null => {
    if (!file) return "Choose an HTML file or a .zip / .tar.gz archive to deploy.";
    const s = slug.trim().toLowerCase();
    if (!s) return "A slug is required — it becomes the site's subdomain.";
    if (!LABEL.test(s)) return "Slug must be lowercase letters, digits, or hyphens.";
    if (!validGroup(normGroup)) {
      return "Group must be up to two slash-separated lowercase labels.";
    }
    return null;
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const v = validate();
    if (v) {
      setError(v);
      return;
    }
    setError(null);
    setBusy(true);
    try {
      const res = await api.publishSite(
        {
          group: normGroup || undefined,
          slug: slug.trim().toLowerCase(),
          title: title.trim() || undefined,
          description: description.trim() || undefined,
          tags: tags
            .split(",")
            .map((t) => t.trim())
            .filter(Boolean),
          hidden,
        },
        file as File,
      );
      setCreated(res);
      onCreated();
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

        {created ? (
          <div className="site-reveal">
            <h2>Site deployed</h2>
            <p className="muted">
              <strong>{created.group ? `${created.group}/` : ""}{created.slug}</strong> is now live at:
            </p>
            <p>
              <a href={created.url} target="_blank" rel="noreferrer">
                {created.url}
              </a>
            </p>
            <div className="actions">
              <button onClick={onClose}>Done</button>
            </div>
          </div>
        ) : (
          <form onSubmit={submit}>
            <h2>Deploy a site</h2>

            <div
              className={`dropzone ${dragging ? "dragging" : ""} ${file ? "filled" : ""}`}
              onDragOver={(e) => {
                e.preventDefault();
                setDragging(true);
              }}
              onDragLeave={() => setDragging(false)}
              onDrop={onDrop}
              onClick={() => inputRef.current?.click()}
              role="button"
              tabIndex={0}
            >
              <input
                ref={inputRef}
                type="file"
                accept={ACCEPT}
                hidden
                onChange={(e) => pick(e.target.files?.[0] ?? null)}
              />
              {file ? (
                <span>
                  <strong>{file.name}</strong> ({Math.ceil(file.size / 1024)} KB)
                </span>
              ) : (
                <span>
                  Drag &amp; drop an <strong>.html</strong>, <strong>.zip</strong>, or{" "}
                  <strong>.tar.gz</strong> here, or click to choose.
                </span>
              )}
            </div>
            {file && !isArchive(file) && (
              <p className="field-hint muted">
                A single HTML file is served as <code>index.html</code> — it must be self-contained
                (inline its CSS/JS). Use an archive to include separate assets.
              </p>
            )}

            <div className="field-row">
              <label className="field">
                <span className="field-label">
                  Group <span className="field-hint muted">(optional)</span>
                </span>
                <input
                  list="site-groups"
                  placeholder="e.g. previews"
                  value={group}
                  onChange={(e) => setGroup(e.target.value)}
                />
                <datalist id="site-groups">
                  {groups.map((g) => (
                    <option key={g} value={g} />
                  ))}
                </datalist>
              </label>

              <label className="field">
                <span className="field-label">Slug</span>
                <input
                  placeholder="e.g. my-site"
                  value={slug}
                  onChange={(e) => setSlug(e.target.value)}
                />
              </label>
            </div>

            <p className="field-hint muted">
              Will be served at{" "}
              <code>
                {(slug.trim().toLowerCase() || "<slug>")}
                {normGroup ? `.${normGroup.split("/").reverse().join(".")}` : ""}.{base}
              </code>
            </p>
            {exists && (
              <p className="field-hint warn">
                ⚠️ A site already exists at this group/slug — deploying will overwrite it.
              </p>
            )}

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

            <label className="field">
              <span className="field-label">
                Tags <span className="field-hint muted">(comma-separated, optional)</span>
              </span>
              <input value={tags} onChange={(e) => setTags(e.target.value)} />
            </label>

            <label className="checkbox">
              <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />
              <span>Hidden — exclude from public listings.</span>
            </label>

            {error && <p className="error">{error}</p>}

            <div className="actions">
              <button type="submit" disabled={busy}>
                {busy ? "Deploying…" : "Deploy"}
              </button>
              <button type="button" className="ghost" onClick={onClose}>
                Cancel
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
