import type { Site } from "../api";
import { siteURL } from "../sitehost";
import { Thumbnail } from "./Thumbnail";

interface Props {
  site: Site;
  base: string;
  onSelect: (s: Site) => void;
}

export function SiteCard({ site, base, onSelect }: Props) {
  const url = siteURL(site, base);
  return (
    <article className={`card${site.hidden ? " hidden" : ""}`}>
      <Thumbnail url={url} preview={site.preview} title={site.title || site.slug} />
      <div className="card-body" onClick={() => onSelect(site)} role="button" tabIndex={0}>
        <h3>{site.title || site.slug}</h3>
        {site.description && <p className="desc">{site.description}</p>}
        <div className="meta">
          {site.tags?.map((t) => (
            <span key={t} className="tag">
              {t}
            </span>
          ))}
          {site.hidden && <span className="tag warn">hidden</span>}
        </div>
      </div>
    </article>
  );
}
