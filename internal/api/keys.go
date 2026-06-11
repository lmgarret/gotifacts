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
		Name   string `json:"name"`
		Admin  bool   `json:"admin"`
		Grants []struct {
			Group       string   `json:"group"`
			Permissions []string `json:"permissions"`
		} `json:"grants"`
		// Legacy fields, accepted for backward compatibility with older clients.
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

	admin := body.Admin
	var grants []store.Grant

	switch {
	case len(body.Grants) > 0:
		for _, g := range body.Grants {
			caps, err := parseGrantCapabilities(g.Permissions)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			grants = append(grants, store.Grant{Group: normalizeGroup(g.Group), Permissions: caps})
		}
	case body.Scope != "":
		// Legacy {scope, group} body: translate to the grant model.
		switch strings.ToLower(strings.TrimSpace(body.Scope)) {
		case "admin":
			admin = true
		case "publish":
			grants = append(grants, store.Grant{
				Group:       normalizeGroup(body.Group),
				Permissions: []keys.Capability{keys.CapPublish},
			})
		default:
			writeError(w, http.StatusBadRequest, "scope must be 'admin' or 'publish'")
			return
		}
	}

	if err := validateKeyShape(admin, grants); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	token, hash, err := keys.Generate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	rec, err := s.store.CreateKey(r.Context(), body.Name, admin, grants, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}
	// Plaintext token is returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     rec.ID,
		"name":   rec.Name,
		"admin":  rec.Admin,
		"grants": rec.Grants,
		"key":    token,
	})
}

// parseGrantCapabilities validates a capability string slice.
func parseGrantCapabilities(perms []string) ([]keys.Capability, error) {
	var caps []keys.Capability
	seen := map[keys.Capability]bool{}
	for _, p := range perms {
		c, err := keys.ParseCapability(p)
		if err != nil {
			return nil, errors.New("unknown capability: " + p)
		}
		if !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}
	if len(caps) == 0 {
		return nil, errors.New("each grant requires at least one capability")
	}
	return caps, nil
}

// validateKeyShape enforces the model's invariants: admin keys carry no grants,
// non-admin keys need at least one grant, and a grant containing a destructive
// capability (unpublish) must be bound to a non-empty group subtree.
func validateKeyShape(admin bool, grants []store.Grant) error {
	if admin {
		if len(grants) > 0 {
			return errors.New("admin keys must not specify grants")
		}
		return nil
	}
	if len(grants) == 0 {
		return errors.New("a non-admin key requires at least one grant")
	}
	for _, g := range grants {
		if g.Group == "" && !keys.OnlyPublish(g.Permissions) {
			return errors.New("a grant with unpublish/rollback/patch must specify a group")
		}
	}
	return nil
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
