import { useState } from "react";
import type { Site } from "../api";
import { faviconURL } from "../api";

interface Props {
  site: Pick<Site, "group" | "slug" | "has_favicon">;
  title: string;
}

// Favicon shows a site's favicon — detected server-side at publish time and
// served from the apex API — falling back to a letter monogram when the site
// has no cached favicon or the image fails to load.
export function Favicon({ site, title }: Props) {
  const [failed, setFailed] = useState(false);
  const letter = (title.trim()[0] || "?").toUpperCase();

  if (!site.has_favicon || failed) {
    return (
      <span className="favicon favicon-fallback" aria-hidden="true">
        {letter}
      </span>
    );
  }

  return (
    <img
      className="favicon"
      src={faviconURL(site)}
      alt=""
      width={16}
      height={16}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}
