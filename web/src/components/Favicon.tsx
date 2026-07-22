import { useState } from "react";

interface Props {
  url: string;
  title: string;
}

// Icon paths to try, in order. Sites rarely ship a root favicon.ico; many
// declare an SVG or PNG instead (gotifacts' own pages use favicon.svg), so we
// probe a small list of common locations before giving up.
const CANDIDATES = ["/favicon.ico", "/favicon.svg", "/favicon.png", "/apple-touch-icon.png"];

// Favicon shows a site's favicon (served from its own origin), falling back to
// a letter monogram once every candidate path fails to load.
export function Favicon({ url, title }: Props) {
  const [idx, setIdx] = useState(0);
  const letter = (title.trim()[0] || "?").toUpperCase();

  if (idx >= CANDIDATES.length) {
    return (
      <span className="favicon favicon-fallback" aria-hidden="true">
        {letter}
      </span>
    );
  }

  return (
    <img
      className="favicon"
      // Key by attempt so React swaps the element (and reissues the request)
      // when we advance to the next candidate after an error.
      key={idx}
      src={`${url}${CANDIDATES[idx]}`}
      alt=""
      width={16}
      height={16}
      loading="lazy"
      onError={() => setIdx((i) => i + 1)}
    />
  );
}
