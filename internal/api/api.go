// Package api wires the management plane (/api/*, forward-auth) and the ingest
// plane (/ingest/*, API-key) onto the apex host, plus the embedded SPA.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

// Server holds dependencies for the apex-host handlers.
type Server struct {
	cfg   *config.Config
	store *store.Store
	authn *auth.Authenticator
	pub   *ingest.Publisher
	spa   http.Handler
	log   *slog.Logger
}

// New constructs the apex Server.
func New(cfg *config.Config, st *store.Store, spa http.Handler, log *slog.Logger) *Server {
	return &Server{
		cfg:   cfg,
		store: st,
		authn: auth.New(cfg, st),
		pub:   ingest.New(cfg, st),
		spa:   spa,
		log:   log,
	}
}

// Handler returns the apex-host HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Management plane (forward-auth).
	mux.HandleFunc("GET /api/me", s.requireUser(s.handleMe))
	mux.HandleFunc("GET /api/sites", s.requireUser(s.handleListSites))
	mux.HandleFunc("POST /api/sites", s.requireAdmin(s.handleUploadSite))
	mux.HandleFunc("PATCH /api/sites/{rest...}", s.requireAdmin(s.handlePatchSite))
	mux.HandleFunc("DELETE /api/sites/{rest...}", s.requireAdmin(s.handleDeleteSite))
	mux.HandleFunc("POST /api/sites/{rest...}", s.requireAdmin(s.handleRollbackSite))
	mux.HandleFunc("GET /api/keys", s.requireAdmin(s.handleListKeys))
	mux.HandleFunc("POST /api/keys", s.requireAdmin(s.handleCreateKey))
	mux.HandleFunc("DELETE /api/keys/{id}", s.requireAdmin(s.handleDeleteKey))

	// Ingest plane (API-key).
	mux.HandleFunc("POST /ingest/sites", s.requireKeyPublish(s.handleIngestPublish))
	mux.HandleFunc("DELETE /ingest/sites/{rest...}", s.requireKeyAdmin(s.handleIngestDelete))

	// Everything else on the apex host is the SPA.
	mux.Handle("/", s.spa)
	return mux
}

// --- middleware ---------------------------------------------------------

type handlerFunc func(http.ResponseWriter, *http.Request, *auth.Principal)

// requireUser admits any authenticated forward-auth principal (viewer+).
func (s *Server) requireUser(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.authn.ForwardAuth(r)
		if p == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		h(w, r, p)
	}
}

// requireAdmin admits only admin forward-auth principals.
func (s *Server) requireAdmin(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.authn.ForwardAuth(r)
		if p == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !p.Admin {
			writeError(w, http.StatusForbidden, "admin privilege required")
			return
		}
		h(w, r, p)
	}
}

// requireKeyPublish admits API-key principals with publish or admin scope.
func (s *Server) requireKeyPublish(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.authn.APIKey(r.Context(), r)
		if p == nil {
			writeError(w, http.StatusUnauthorized, "valid API key required")
			return
		}
		h(w, r, p)
	}
}

// requireKeyAdmin admits only admin-scoped API-key principals.
func (s *Server) requireKeyAdmin(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.authn.APIKey(r.Context(), r)
		if p == nil {
			writeError(w, http.StatusUnauthorized, "valid API key required")
			return
		}
		if !p.Admin {
			writeError(w, http.StatusForbidden, "admin-scoped key required")
			return
		}
		h(w, r, p)
	}
}

// --- helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// parseSitePath splits a {rest...} match into a validated SitePath. An optional
// trailing suffix (e.g. "rollback") may be required and is stripped first.
func parseSitePath(rest, requireSuffix string) (router.SitePath, error) {
	rest = strings.Trim(rest, "/")
	if requireSuffix != "" {
		trimmed := strings.TrimSuffix(rest, "/"+requireSuffix)
		if trimmed == rest {
			return router.SitePath{}, errBadSuffix
		}
		rest = trimmed
	}
	segs := strings.Split(rest, "/")
	slug := segs[len(segs)-1]
	group := strings.Join(segs[:len(segs)-1], "/")
	return router.NewSitePath(group, slug)
}
