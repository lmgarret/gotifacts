import { useState } from "react";
import type { FileNode } from "../api";

interface Props {
  node: FileNode;
  depth: number;
  // downloadURL returns the href for downloading a file at the given path.
  downloadURL: (path: string) => string;
}

// formatSize renders a byte count in human-friendly units.
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let n = bytes / 1024;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(n < 10 ? 1 : 0)} ${units[i]}`;
}

// FileTree renders a revision's files as a collapsible tree. The root node
// (empty name) is not drawn; its children are rendered directly.
export function FileTree({ node, depth, downloadURL }: Props) {
  if (node.name === "" && depth === 0) {
    if (!node.children || node.children.length === 0) {
      return <p className="muted empty">This revision has no files.</p>;
    }
    return (
      <ul className="filetree">
        {node.children.map((c) => (
          <FileTree key={c.path} node={c} depth={1} downloadURL={downloadURL} />
        ))}
      </ul>
    );
  }

  return node.dir ? (
    <FileDir node={node} depth={depth} downloadURL={downloadURL} />
  ) : (
    <li className="file-row" style={{ paddingLeft: `${depth * 1}rem` }}>
      <span className="file-name">{node.name}</span>
      {node.size != null && <span className="file-size muted">{formatSize(node.size)}</span>}
      <a className="file-dl" href={downloadURL(node.path)} download>
        Download
      </a>
    </li>
  );
}

function FileDir({ node, depth, downloadURL }: Props) {
  const [open, setOpen] = useState(depth <= 1);
  return (
    <li className="file-dir">
      <button
        className="file-dir-header"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        style={{ paddingLeft: `${depth * 1}rem` }}
      >
        <span className="chevron">{open ? "▾" : "▸"}</span>
        <span className="file-name">{node.name}</span>
      </button>
      {open && node.children && (
        <ul className="filetree">
          {node.children.map((c) => (
            <FileTree key={c.path} node={c} depth={depth + 1} downloadURL={downloadURL} />
          ))}
        </ul>
      )}
    </li>
  );
}
