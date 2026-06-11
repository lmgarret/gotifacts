package api

import (
	"errors"
	"net/http"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

// handleIngestPublish is the machine publish endpoint. publish() authorizes the
// publish capability against the target group.
func (s *Server) handleIngestPublish(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.publish(w, r, p)
}

// handleIngestDelete removes a site via a key holding the unpublish capability
// on the site's group (automation cleanup, e.g. PR-preview teardown).
func (s *Server) handleIngestDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	sp, err := parseSitePath(r.PathValue("rest"), "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid site path")
		return
	}
	if !p.Can(keys.CapUnpublish, sp.Group) {
		writeError(w, http.StatusForbidden, "key not permitted to unpublish from this group")
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

// handleIngestPatch edits site metadata via a key holding the patch capability.
func (s *Server) handleIngestPatch(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	sp, err := parseSitePath(r.PathValue("rest"), "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid site path")
		return
	}
	if !p.Can(keys.CapPatch, sp.Group) {
		writeError(w, http.StatusForbidden, "key not permitted to patch this group")
		return
	}
	s.patchSite(w, r, sp.Group, sp.Slug)
}

// handleIngestRollback restores a site's previous version via a key holding the
// rollback capability. The path carries a trailing "/rollback" suffix.
func (s *Server) handleIngestRollback(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	sp, err := parseSitePath(r.PathValue("rest"), "rollback")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rollback path")
		return
	}
	if !p.Can(keys.CapRollback, sp.Group) {
		writeError(w, http.StatusForbidden, "key not permitted to roll back this group")
		return
	}
	s.rollbackSite(w, r, sp)
}
