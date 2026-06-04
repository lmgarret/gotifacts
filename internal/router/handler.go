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

// ServeHTTP dispatches by host.
func (d *Dispatch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if IsBaseHost(r.Host, d.cfg.BaseDomain) {
		d.apex.ServeHTTP(w, r)
		return
	}
	d.sites.ServeHTTP(w, r)
}
