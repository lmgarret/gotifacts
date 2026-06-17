package api

import (
	"errors"
	"net/http"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/store"
)

// handleListConnections lists active MCP connections (one per consent), so an
// admin can see which clients hold a live grant and revoke them. Admin-gated.
func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	conns, err := s.store.ListConnections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connections")
		return
	}
	if conns == nil {
		conns = []store.Connection{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": conns})
}

// handleRevokeConnection revokes an MCP connection by deleting all of its
// tokens, so the connector immediately loses access. Admin-gated.
func (s *Server) handleRevokeConnection(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	id := r.PathValue("id")
	if err := s.store.DeleteConnection(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to revoke connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
