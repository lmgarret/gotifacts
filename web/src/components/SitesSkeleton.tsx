import type { CSSProperties } from "react";

interface Props {
  layout: "card" | "table";
  // How many placeholder items to render. A handful is enough to fill the
  // first viewport without implying a specific site count.
  count?: number;
}

// Slightly varied line widths keep the placeholders from looking like a rigid
// grid while the real content loads.
const TITLE_WIDTHS = ["70%", "55%", "62%", "48%"];
const DESC_WIDTHS = ["90%", "80%", "95%", "72%"];

// SitesSkeleton renders shimmering placeholders that mirror the card grid or
// the sites table, so the portal keeps its shape while the site list loads
// instead of flashing an empty state.
export function SitesSkeleton({ layout, count = 8 }: Props) {
  const items = Array.from({ length: count });

  if (layout === "table") {
    return (
      <div className="table-wrap" aria-hidden="true">
        <table className="sites-table">
          <tbody>
            {items.map((_, i) => (
              <tr key={i} className="skeleton-row">
                <td className="col-icon">
                  <span className="sk sk-favicon" />
                </td>
                <td className="col-title">
                  <span className="sk sk-line" style={width(TITLE_WIDTHS[i % TITLE_WIDTHS.length])} />
                  <span className="sk sk-line sk-sub" style={width(DESC_WIDTHS[i % DESC_WIDTHS.length])} />
                </td>
                <td className="col-group">
                  <span className="sk sk-line" style={width("60%")} />
                </td>
                <td className="col-slug">
                  <span className="sk sk-line" style={width("70%")} />
                </td>
                <td className="col-date">
                  <span className="sk sk-line" style={width("4rem")} />
                </td>
                <td className="col-date">
                  <span className="sk sk-line" style={width("4rem")} />
                </td>
                <td className="col-size">
                  <span className="sk sk-line" style={width("2.5rem")} />
                </td>
                <td className="col-tags">
                  <span className="sk sk-tag" />
                  <span className="sk sk-tag" />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <div className="cards" aria-hidden="true">
      {items.map((_, i) => (
        <article key={i} className="card skeleton-card">
          <div className="thumb sk" />
          <div className="card-body">
            <span className="sk sk-line sk-title" style={width(TITLE_WIDTHS[i % TITLE_WIDTHS.length])} />
            <span className="sk sk-line" style={width(DESC_WIDTHS[i % DESC_WIDTHS.length])} />
            <span className="sk sk-line" style={width("60%")} />
            <div className="meta">
              <span className="sk sk-tag" />
              <span className="sk sk-tag" />
            </div>
          </div>
        </article>
      ))}
    </div>
  );
}

function width(w: string): CSSProperties {
  return { width: w };
}
