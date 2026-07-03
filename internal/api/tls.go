package api

import (
	"context"
	"net/http"

	"github.com/lmgarret/gotifacts/internal/router"
)

// TLSCheckHandler answers Caddy's on-demand TLS "ask" endpoint. Caddy issues a
// request like GET <path>?domain=<sni> before obtaining a certificate for a
// host seen in a TLS handshake; it treats a 2xx response as permission to
// proceed and anything else as a refusal. We approve only hosts gotifacts
// recognizes — the apex or a live registered site — so an attacker cannot make
// the server request certificates for arbitrary domains (which could exhaust
// disk and get the server rate-limited by the CA).
func (s *Server) TLSCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		if domain == "" {
			writeError(w, http.StatusBadRequest, "missing domain")
			return
		}
		if s.allowOnDemandTLS(r.Context(), domain) {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeError(w, http.StatusForbidden, "unknown host")
	}
}

// allowOnDemandTLS reports whether a certificate should be obtained for domain:
// true for the apex host, or a syntactically valid site host that maps to an
// existing, non-deleted site in the registry.
func (s *Server) allowOnDemandTLS(ctx context.Context, domain string) bool {
	if router.IsBaseHost(domain, s.cfg.BaseDomain) {
		return true
	}
	sp, err := router.ParseHost(domain, s.cfg.BaseDomain)
	if err != nil {
		return false
	}
	// GetSite filters soft-deleted sites, so unpublished hosts are refused.
	_, err = s.store.GetSite(ctx, sp.Group, sp.Slug)
	return err == nil
}
