package api

import (
	"errors"
	"net/http"
	"strings"

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
	if !p.Can(keys.CapUnpublish, sp.Group, sp.Slug) {
		writeError(w, http.StatusForbidden, "key not permitted to unpublish from this group")
		return
	}
	if err := s.pub.Unpublish(r.Context(), sp.Group, sp.Slug); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "site not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unpublish site")
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
	if !p.Can(keys.CapPatch, sp.Group, sp.Slug) {
		writeError(w, http.StatusForbidden, "key not permitted to patch this group")
		return
	}
	s.patchSite(w, r, sp.Group, sp.Slug)
}

// handleIngestAction dispatches POST /ingest/sites/{path}/{action} based on the
// trailing action suffix: "rollback" or "purge".
func (s *Server) handleIngestAction(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	rest := r.PathValue("rest")
	switch {
	case hasSuffix(rest, "purge"):
		sp, err := parseSitePath(rest, "purge")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid purge path")
			return
		}
		if !p.Can(keys.CapPurge, sp.Group, sp.Slug) {
			writeError(w, http.StatusForbidden, "key not permitted to purge from this group")
			return
		}
		if err := s.pub.Purge(r.Context(), sp.Group, sp.Slug); errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "site not found or not deleted")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to purge site")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default: // "rollback"
		sp, err := parseSitePath(rest, "rollback")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid rollback path")
			return
		}
		if !p.Can(keys.CapRollback, sp.Group, sp.Slug) {
			writeError(w, http.StatusForbidden, "key not permitted to roll back this group")
			return
		}
		s.rollbackSite(w, r, sp)
	}
}

// hasSuffix reports whether the last path segment of rest equals suffix.
func hasSuffix(rest, suffix string) bool {
	segs := strings.Split(strings.Trim(rest, "/"), "/")
	return len(segs) > 0 && segs[len(segs)-1] == suffix
}
