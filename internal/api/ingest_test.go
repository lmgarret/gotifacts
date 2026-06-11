package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	t.Setenv("GOTIFACTS_BASE_DOMAIN", "example.com")
	t.Setenv("GOTIFACTS_ADMIN_USERS", "alice")
	t.Setenv("GOTIFACTS_TRUSTED_PROXIES", "127.0.0.1/32")
	t.Setenv("GOTIFACTS_DATA_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir()+"/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, st, http.NotFoundHandler(), log), st
}

func mintKey(t *testing.T, st *store.Store, admin bool, grants []store.Grant) string {
	t.Helper()
	tok, hash, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateKey(context.Background(), "k", admin, grants, nil, hash); err != nil {
		t.Fatal(err)
	}
	return tok
}

// reqStatus issues an authenticated request and returns the response status.
func reqStatus(t *testing.T, h http.Handler, method, target, token string) int {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// TestIngestCapabilityGating verifies the ingest plane authorizes each action
// against the key's grants. The site does not exist, so an authorized request
// surfaces a downstream 404/400 (never 403); an unauthorized one is 403.
func TestIngestCapabilityGating(t *testing.T) {
	srv, st := testServer(t)
	h := srv.Handler()

	// Key may publish+unpublish under "previews" only.
	tok := mintKey(t, st, false, []store.Grant{{
		Kind:        store.GrantGroup,
		Target:      "previews",
		Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish},
	}})

	cases := []struct {
		name       string
		method     string
		target     string
		token      string
		wantStatus int
	}{
		{"unpublish in scope -> not 403 (404 missing site)", http.MethodDelete, "http://example.com/ingest/sites/previews/pr-1", tok, http.StatusNotFound},
		{"unpublish out of scope -> 403", http.MethodDelete, "http://example.com/ingest/sites/prod/app", tok, http.StatusForbidden},
		{"unpublish the group's own subdomain -> not 403 (404 missing)", http.MethodDelete, "http://example.com/ingest/sites/previews", tok, http.StatusNotFound},
		{"unpublish unrelated apex site -> 403", http.MethodDelete, "http://example.com/ingest/sites/other", tok, http.StatusForbidden},
		{"patch without cap -> 403", http.MethodPatch, "http://example.com/ingest/sites/previews/pr-1", tok, http.StatusForbidden},
		{"rollback without cap -> 403", http.MethodPost, "http://example.com/ingest/sites/previews/pr-1/rollback", tok, http.StatusForbidden},
		{"missing token -> 401", http.MethodDelete, "http://example.com/ingest/sites/previews/pr-1", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got int
			if c.token == "" {
				r := httptest.NewRequest(c.method, c.target, nil)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				got = w.Code
			} else {
				got = reqStatus(t, h, c.method, c.target, c.token)
			}
			if got != c.wantStatus {
				t.Fatalf("status = %d, want %d", got, c.wantStatus)
			}
		})
	}

	// An admin key passes the gate for every action (downstream 404, not 403).
	admTok := mintKey(t, st, true, nil)
	if s := reqStatus(t, h, http.MethodPatch, "http://example.com/ingest/sites/anything/x", admTok); s == http.StatusForbidden {
		t.Fatal("admin key must not be forbidden")
	}
}
