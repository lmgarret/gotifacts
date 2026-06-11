package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

var errBadSuffix = errors.New("path missing required suffix")

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request, p *auth.Principal) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":        p.User,
		"is_admin":    p.Admin,
		"base_domain": s.cfg.BaseDomain,
	})
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	f := store.ListFilter{
		Query:       q.Get("q"),
		Tag:         q.Get("tag"),
		Group:       q.Get("group"),
		Sort:        q.Get("sort"),
		IncludeHide: p.Admin && q.Get("hidden") == "true",
		Limit:       limit,
		Offset:      offset,
	}
	sites, err := s.store.ListSites(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list sites")
		return
	}
	if sites == nil {
		sites = []store.Site{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sites": sites,
		"tree":  BuildTree(sites),
		"count": len(sites),
	})
}

// handleUploadSite is the admin manual upload (same body as ingest).
func (s *Server) handleUploadSite(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.publish(w, r, p)
}

func (s *Server) handlePatchSite(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	sp, err := parseSitePath(r.PathValue("rest"), "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid site path")
		return
	}
	s.patchSite(w, r, sp.Group, sp.Slug)
}

// patchSite decodes a metadata patch body and applies it to the named site,
// writing the JSON response. Shared by the management and ingest planes.
func (s *Server) patchSite(w http.ResponseWriter, r *http.Request, group, slug string) {
	var body struct {
		Title       *string   `json:"title"`
		Description *string   `json:"description"`
		Date        *string   `json:"date"`
		Tags        *[]string `json:"tags"`
		Repo        *string   `json:"repo"`
		Preview     *string   `json:"preview"`
		Hidden      *bool     `json:"hidden"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	site, err := s.store.PatchSite(r.Context(), group, slug, store.SitePatch{
		Title:       body.Title,
		Description: body.Description,
		Date:        body.Date,
		Tags:        body.Tags,
		Repo:        body.Repo,
		Preview:     body.Preview,
		Hidden:      body.Hidden,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update site")
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
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

func (s *Server) handleRollbackSite(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	sp, err := parseSitePath(r.PathValue("rest"), "rollback")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rollback path")
		return
	}
	s.rollbackSite(w, r, sp)
}

// rollbackSite restores the named site's previous version and writes the
// resulting site as JSON. Shared by the management and ingest planes.
func (s *Server) rollbackSite(w http.ResponseWriter, r *http.Request, sp router.SitePath) {
	if err := s.pub.Rollback(r.Context(), sp); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	site, err := s.store.GetSite(r.Context(), sp.Group, sp.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rolled back but registry read failed")
		return
	}
	writeJSON(w, http.StatusOK, site)
}

// publish performs a multipart publish shared by admin upload and ingest.
func (s *Server) publish(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	meta, kind, content, cleanup, err := parseMultipartPublish(w, r, s.cfg.MaxUploadBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()

	if !p.CanPublishTo(normalizeGroup(meta.Group)) {
		writeError(w, http.StatusForbidden, "key not permitted to publish to this group")
		return
	}

	res, _, err := s.pub.Publish(r.Context(), meta, kind, content)
	if err != nil {
		s.log.Warn("publish failed", "group", meta.Group, "slug", meta.Slug, "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// deleteSite removes registry row and on-disk content for a site.
func (s *Server) deleteSite(r *http.Request, group, slug string) error {
	if err := s.store.DeleteSite(r.Context(), group, slug); err != nil {
		return err
	}
	s.removeSiteDir(group, slug)
	return nil
}
