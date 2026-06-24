import { useState } from "react";

interface Props {
  url: string;
  title: string;
}

// Favicon shows a site's favicon (served from its own origin), falling back to
// a letter monogram when the icon is missing or fails to load.
export function Favicon({ url, title }: Props) {
  const [failed, setFailed] = useState(false);
  const letter = (title.trim()[0] || "?").toUpperCase();

  if (failed) {
    return (
      <span className="favicon favicon-fallback" aria-hidden="true">
        {letter}
      </span>
    );
  }

  return (
    <img
      className="favicon"
      src={`${url}/favicon.ico`}
      alt=""
      width={16}
      height={16}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}
