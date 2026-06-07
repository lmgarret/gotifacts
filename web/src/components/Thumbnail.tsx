import { useEffect, useRef, useState } from "react";

// A bounded queue limits how many live iframe previews load concurrently, so a
// portal with many sites does not spawn dozens of simultaneous page loads.
const MAX_CONCURRENT = 4;
let active = 0;
const waiters: Array<() => void> = [];

function acquire(): Promise<void> {
  if (active < MAX_CONCURRENT) {
    active++;
    return Promise.resolve();
  }
  return new Promise((resolve) => waiters.push(resolve));
}

function release() {
  const next = waiters.shift();
  if (next) {
    next();
  } else {
    active = Math.max(0, active - 1);
  }
}

interface Props {
  url: string;
  preview?: string;
  title: string;
}

// Thumbnail renders a sandboxed, scaled-down live iframe of a site (or a
// preview image when provided), loaded lazily once scrolled into view.
export function Thumbnail({ url, preview, title }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const releasedRef = useRef(false);
  const [visible, setVisible] = useState(false);
  const [src, setSrc] = useState<string | null>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          obs.disconnect();
        }
      },
      { rootMargin: "200px" },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, []);

  // releaseOnce frees the shared slot at most once (on load or unmount).
  const releaseOnce = () => {
    if (!releasedRef.current) {
      releasedRef.current = true;
      release();
    }
  };

  useEffect(() => {
    if (!visible || preview) return;
    let cancelled = false;
    acquire().then(() => {
      if (cancelled) {
        releaseOnce();
      } else {
        setSrc(url);
      }
    });
    return () => {
      cancelled = true;
      releaseOnce();
    };
  }, [visible, url, preview]);

  return (
    <div className="thumb" ref={ref}>
      {preview ? (
        visible && <img src={preview} alt={title} loading="lazy" />
      ) : src ? (
        <iframe
          src={src}
          title={title}
          loading="lazy"
          sandbox="allow-scripts allow-same-origin"
          scrolling="no"
          tabIndex={-1}
          onLoad={releaseOnce}
        />
      ) : (
        <div className="thumb-placeholder">{title}</div>
      )}
      {/* Transparent overlay: clicks open the site, never interact with it. */}
      <a className="thumb-overlay" href={url} target="_blank" rel="noopener noreferrer" aria-label={title} />
    </div>
  );
}
