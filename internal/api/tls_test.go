package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/store"
)

// tlsCheckStatus builds a Server over a store seeded with a single site
// (claude/app), runs the ask endpoint for domain, and returns the status code.
func newTLSServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.UpsertSite(context.Background(), "claude", "app", store.SiteInput{Title: "App"}); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	srv, err := New(
		&config.Config{BaseDomain: "example.com", DataDir: t.TempDir()},
		st, spa, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return srv, st
}

func askStatus(t *testing.T, srv *Server, query string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/_gotifacts/tls-check"+query, nil)
	rec := httptest.NewRecorder()
	srv.TLSCheckHandler()(rec, req)
	return rec.Code
}

func TestTLSCheck(t *testing.T) {
	srv, _ := newTLSServer(t)

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"apex", "?domain=example.com", http.StatusOK},
		{"live site", "?domain=app.claude.example.com", http.StatusOK},
		{"unknown site", "?domain=ghost.claude.example.com", http.StatusForbidden},
		{"outside base", "?domain=app.claude.other.com", http.StatusForbidden},
		{"too deep", "?domain=a.b.c.d.example.com", http.StatusForbidden},
		{"missing domain", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := askStatus(t, srv, tc.query); got != tc.want {
				t.Fatalf("domain %q: status = %d, want %d", tc.query, got, tc.want)
			}
		})
	}
}

func TestTLSCheckRejectsDeletedSite(t *testing.T) {
	srv, st := newTLSServer(t)

	if got := askStatus(t, srv, "?domain=app.claude.example.com"); got != http.StatusOK {
		t.Fatalf("live site: status = %d, want 200", got)
	}
	if err := st.SoftDeleteSite(context.Background(), "claude", "app"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	if got := askStatus(t, srv, "?domain=app.claude.example.com"); got != http.StatusForbidden {
		t.Fatalf("deleted site: status = %d, want 403", got)
	}
}
