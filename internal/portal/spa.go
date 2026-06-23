package portal

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPA serves the embedded single-page application, falling back to index.html
// for client-side routes (non-asset paths).
type SPA struct {
	files fs.FS
}

// NewSPA returns an SPA handler over the given embedded file system.
func NewSPA(files fs.FS) *SPA {
	return &SPA{files: files}
}

// ServeHTTP serves a static asset if present, else the SPA shell.
func (s *SPA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	// Redirect legacy .ico requests to the SVG favicon so MCP connector UIs
	// (which probe /favicon.ico) pick up the gotifacts icon.
	if name == "favicon.ico" {
		http.Redirect(w, r, "/favicon.svg", http.StatusMovedPermanently)
		return
	}
	if f, err := s.files.Open(name); err == nil {
		defer func() { _ = f.Close() }()
		if st, err := f.Stat(); err == nil && !st.IsDir() {
			http.ServeFileFS(w, r, s.files, name)
			return
		}
	}
	// Fallback to SPA shell for client-side routes.
	s.serveIndex(w, r)
}

func (s *SPA) serveIndex(w http.ResponseWriter, _ *http.Request) {
	f, err := s.files.Open("index.html")
	if err != nil {
		http.Error(w, "frontend not built", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.Copy(w, f)
}
