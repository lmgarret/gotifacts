// Package portal serves static site content (host-routed) and the embedded
// management SPA.
package portal

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/router"
)

// SiteServer serves files for non-apex hosts from the sites directory.
type SiteServer struct {
	cfg *config.Config
}

// NewSiteServer returns a SiteServer.
func NewSiteServer(cfg *config.Config) *SiteServer {
	return &SiteServer{cfg: cfg}
}

// ServeHTTP maps the request host to a site directory and serves static files.
// There is no SPA fallback: a missing file yields a clean 404.
func (s *SiteServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sp, err := router.ParseHost(r.Host, s.cfg.BaseDomain)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	root := filepath.Join(s.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		http.NotFound(w, r)
		return
	}

	upath := r.URL.Path
	if !strings.HasPrefix(upath, "/") {
		upath = "/" + upath
	}
	clean := filepath.Clean(upath)
	if clean == "/" {
		clean = "/index.html"
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	// Guard against traversal escaping the site root.
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}

	fi, err := os.Stat(target)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if fi.IsDir() {
		target = filepath.Join(target, "index.html")
		if _, err := os.Stat(target); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	setSiteCacheHeaders(w, target)
	http.ServeFile(w, r, target)
}

// setSiteCacheHeaders applies conservative caching: long-lived for fingerprinted
// assets, no-cache for HTML so updates appear immediately.
func setSiteCacheHeaders(w http.ResponseWriter, path string) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}
