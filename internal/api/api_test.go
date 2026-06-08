package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/store"
)

func newTestServer(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "spa")
	})
	srv, err := New(cfg, st, spa, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return srv.Handler()
}

func TestMCPDisabledIsInert(t *testing.T) {
	h := newTestServer(t, &config.Config{BaseDomain: "example.com", DataDir: t.TempDir()})
	srv := httptest.NewServer(h)
	defer srv.Close()

	// With MCP off, the discovery and MCP paths are unregistered, so the SPA
	// catch-all handles them.
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/mcp"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "spa" {
			t.Fatalf("%s: expected SPA fallthrough, got %q", path, body)
		}
	}
}

func TestMCPEnabledMountsRoutes(t *testing.T) {
	h := newTestServer(t, &config.Config{
		BaseDomain: "example.com",
		DataDir:    t.TempDir(),
		MCPEnabled: true,
		MCPGroup:   "claude",
		AdminUsers: []string{"tester"},
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var as map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&as); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if as["issuer"] != "https://example.com" {
		t.Fatalf("issuer = %v (MCP routes not mounted?)", as["issuer"])
	}
}
