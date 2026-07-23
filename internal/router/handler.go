package router

import (
	"net/http"

	"github.com/lmgarret/gotifacts/internal/config"
)

// Dispatch routes requests by Host: the apex host goes to the apex handler
// (portal + API + ingest); any other host is served as static site content.
type Dispatch struct {
	cfg   *config.Config
	apex  http.Handler
	sites http.Handler
}

// NewDispatch builds the top-level host dispatcher.
func NewDispatch(cfg *config.Config, apex, sites http.Handler) *Dispatch {
	return &Dispatch{cfg: cfg, apex: apex, sites: sites}
}

// ServeHTTP dispatches by host. The apex of any configured domain (canonical or
// alias) goes to the apex handler; every other host is served as static site
// content, which 404s if the host is not under any configured domain.
func (d *Dispatch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if base, ok := MatchDomain(r.Host, d.cfg.AllDomains()); ok && IsBaseHost(r.Host, base) {
		d.apex.ServeHTTP(w, r)
		return
	}
	d.sites.ServeHTTP(w, r)
}
