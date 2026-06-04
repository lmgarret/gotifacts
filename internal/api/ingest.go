package api

import (
	"errors"
	"net/http"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/store"
)

// handleIngestPublish is the machine publish endpoint (publish/admin key).
func (s *Server) handleIngestPublish(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.publish(w, r, p)
}

// handleIngestDelete removes a site via an admin-scoped key (automation cleanup).
func (s *Server) handleIngestDelete(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	sp, err := parseSitePath(r.PathValue("rest"), "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid site path")
		return
	}
	if err := s.deleteSite(r, sp.Group, sp.Slug); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "site not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete site")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
