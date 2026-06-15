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
	"github.com/lmgarret/gotifacts/internal/mcpserver"
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
	mcp   *mcpserver.Service
}

// New constructs the apex Server. When MCP is enabled, the OAuth-protected MCP
// service is built and its routes are registered by Handler.
func New(cfg *config.Config, st *store.Store, spa http.Handler, log *slog.Logger) (*Server, error) {
	pub := ingest.New(cfg, st)
	s := &Server{
		cfg:   cfg,
		store: st,
		authn: auth.New(cfg, st),
		pub:   pub,
		spa:   spa,
		log:   log,
	}
	if cfg.MCPEnabled {
		m, err := mcpserver.New(cfg, st, pub, log)
		if err != nil {
			return nil, err
		}
		s.mcp = m
	}
	return s, nil
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

	// Ingest plane (API-key). Each handler authorizes the specific capability
	// against the request's group; admin keys pass unconditionally.
	mux.HandleFunc("POST /ingest/sites", s.requireKey(s.handleIngestPublish))
	mux.HandleFunc("POST /ingest/sites/{rest...}", s.requireKey(s.handleIngestRollback))
	mux.HandleFunc("PATCH /ingest/sites/{rest...}", s.requireKey(s.handleIngestPatch))
	mux.HandleFunc("DELETE /ingest/sites/{rest...}", s.requireKey(s.handleIngestDelete))

	// MCP plane (OAuth). The browser-facing consent endpoint is forward-auth'd;
	// the machine-facing endpoints (metadata, register, token, /mcp) are not.
	if s.mcp != nil {
		mux.HandleFunc("GET /mcp/oauth/authorize", s.requireUser(s.mcp.HandleAuthorize))
		mux.HandleFunc("POST /mcp/oauth/authorize", s.requireUser(s.mcp.HandleAuthorize))
		// Connection management lives on the forward-auth management plane.
		mux.HandleFunc("GET /api/mcp/connections", s.requireAdmin(s.handleListConnections))
		mux.HandleFunc("DELETE /api/mcp/connections/{id}", s.requireAdmin(s.handleRevokeConnection))
		s.mcp.RegisterPublic(mux)
	}

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

// requireKey admits any valid API-key principal. The specific capability is
// authorized inside each handler, since it depends on the request's group.
func (s *Server) requireKey(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.authn.APIKey(r.Context(), r)
		if p == nil {
			writeError(w, http.StatusUnauthorized, "valid API key required")
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
