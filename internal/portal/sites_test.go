package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
)

// writeSite writes index.html into a site's @site content leaf.
func writeSite(t *testing.T, cfg *config.Config, dir, body string) {
	t.Helper()
	full := filepath.Join(cfg.SitesDir(), filepath.FromSlash(dir), "@site")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+host+path, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSiteServerFlatAndGroupCoexist(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), BaseDomain: "example.com"}
	writeSite(t, cfg, "decks", "flat")
	writeSite(t, cfg, "decks/pr-6", "preview")
	srv := NewSiteServer(cfg)

	// The flat site serves at the apex sub-label.
	if w := get(t, srv, "decks.example.com", "/"); w.Code != http.StatusOK || w.Body.String() != "flat" {
		t.Fatalf("flat site: code=%d body=%q", w.Code, w.Body.String())
	}
	// The group member serves on its own host.
	if w := get(t, srv, "pr-6.decks.example.com", "/"); w.Code != http.StatusOK || w.Body.String() != "preview" {
		t.Fatalf("member site: code=%d body=%q", w.Code, w.Body.String())
	}
	// A path on the flat host must NOT expose the member's files (no leak):
	// content lives under @site, so decks.example.com/pr-6 has nothing to serve.
	if w := get(t, srv, "decks.example.com", "/pr-6"); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for flat-host subpath, got %d", w.Code)
	}
	if w := get(t, srv, "decks.example.com", "/pr-6/index.html"); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for flat-host member file, got %d", w.Code)
	}
}
