package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	ks, err := s.store.ListKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list keys")
		return
	}
	if ks == nil {
		ks = []store.APIKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": ks})
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	var body struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
		Group string `json:"group"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	scope, err := keys.ParseScope(body.Scope)
	if err != nil {
		writeError(w, http.StatusBadRequest, "scope must be 'admin' or 'publish'")
		return
	}
	group := normalizeGroup(body.Group)
	if scope == keys.ScopeAdmin && group != "" {
		writeError(w, http.StatusBadRequest, "group restriction is only valid for publish scope")
		return
	}

	token, hash, err := keys.Generate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	rec, err := s.store.CreateKey(r.Context(), body.Name, scope, group, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}
	// Plaintext token is returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                rec.ID,
		"name":              rec.Name,
		"scope":             rec.Scope,
		"group_restriction": rec.GroupRestriction,
		"key":               token,
	})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if err := s.store.DeleteKey(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
