package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	ks, err := s.store.ListKeys(r.Context())
	if err != nil {
		s.log.Error("failed to list keys", "user", p.User, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list keys")
		return
	}
	if ks == nil {
		ks = []store.APIKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": ks})
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request, actor *auth.Principal) {
	var body struct {
		Name   string `json:"name"`
		Admin  bool   `json:"admin"`
		Grants []struct {
			Kind        string   `json:"kind"`
			Target      string   `json:"target"`
			Permissions []string `json:"permissions"`
		} `json:"grants"`
		// ExpiresAt is an optional RFC3339 (or YYYY-MM-DD) instant; omit/empty for
		// a key that never expires.
		ExpiresAt string `json:"expires_at"`
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

	expiresAt, err := parseExpiry(body.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
			grant, err := buildGrant(store.ParseGrantKind(g.Kind), g.Target, caps)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			grants = append(grants, grant)
		}
	case body.Scope != "":
		// Legacy {scope, group} body: translate to the grant model.
		switch strings.ToLower(strings.TrimSpace(body.Scope)) {
		case "admin":
			admin = true
		case "publish":
			grants = append(grants, store.Grant{
				Kind:        store.GrantGroup,
				Target:      normalizeGroup(body.Group),
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
		s.log.Error("failed to generate API key", "user", actor.User, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	rec, err := s.store.CreateKey(r.Context(), body.Name, admin, grants, expiresAt, hash)
	if err != nil {
		s.log.Error("failed to create API key", "user", actor.User, "name", body.Name, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}
	s.log.Info("API key created", "user", actor.User, "key_id", rec.ID, "name", rec.Name, "admin", rec.Admin, "grants", len(rec.Grants))
	// Plaintext token is returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         rec.ID,
		"name":       rec.Name,
		"admin":      rec.Admin,
		"grants":     rec.Grants,
		"expires_at": rec.ExpiresAt,
		"key":        token,
	})
}

// parseExpiry parses an optional expiry instant. It accepts RFC3339 or a
// date-only YYYY-MM-DD (interpreted as end of that UTC day). An empty string
// means "never". The instant must be in the future.
func parseExpiry(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var t time.Time
	var err error
	if len(s) == len("2006-01-02") {
		t, err = time.Parse("2006-01-02", s)
		if err == nil {
			t = t.Add(24*time.Hour - time.Second) // end of that day, UTC
		}
	} else {
		t, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return nil, errors.New("expires_at must be RFC3339 or YYYY-MM-DD")
	}
	if !t.After(time.Now()) {
		return nil, errors.New("expires_at must be in the future")
	}
	return &t, nil
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

// buildGrant normalizes and validates a single grant's target for its kind. A
// site grant requires a valid site path; a group grant accepts a valid group
// path or an empty target (meaning "all sites", i.e. global).
func buildGrant(kind store.GrantKind, target string, caps []keys.Capability) (store.Grant, error) {
	t := normalizeGroup(target)
	if kind == store.GrantSite {
		if t == "" {
			return store.Grant{}, errors.New("a site grant requires a target")
		}
		sp, err := parseSitePath(t, "")
		if err != nil {
			return store.Grant{}, errors.New("invalid site target: " + target)
		}
		t = sp.Dir()
	} else if t != "" {
		sp, err := parseSitePath(t, "")
		if err != nil {
			return store.Grant{}, errors.New("invalid group target: " + target)
		}
		t = sp.Dir()
	}
	return store.Grant{Kind: kind, Target: t, Permissions: caps}, nil
}

// validateKeyShape enforces the key-level invariants: admin keys carry no
// grants; non-admin keys need at least one grant. Per-grant validity (target
// shape, capability set) is enforced when each grant is built.
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
	return nil
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request, actor *auth.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if err := s.store.DeleteKey(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	} else if err != nil {
		s.log.Error("failed to delete API key", "user", actor.User, "key_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}
	s.log.Info("API key revoked", "user", actor.User, "key_id", id)
	w.WriteHeader(http.StatusNoContent)
}
