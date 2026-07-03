package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func reqSPA(t *testing.T, spa *SPA, path string) (string, int) {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, r)
	return w.Body.String(), w.Code
}

func TestSPAServesShellForClientRoutes(t *testing.T) {
	files := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html>SHELL")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	spa := NewSPA(files)

	// A built asset is served directly, not the shell.
	if body, code := reqSPA(t, spa, "/assets/app.js"); code != http.StatusOK || body != "console.log(1)" {
		t.Fatalf("asset: code=%d body=%q", code, body)
	}

	// Client-side routes (the portal's settings deep links) fall back to the
	// shell so a full-page load of e.g. /settings/api-keys boots the SPA and the
	// client router resolves the view.
	for _, p := range []string{"/", "/settings/api-keys", "/settings/connections", "/settings/trash", "/s/decks/pr-6"} {
		body, code := reqSPA(t, spa, p)
		if code != http.StatusOK || body != "<!doctype html>SHELL" {
			t.Fatalf("route %s: code=%d body=%q", p, code, body)
		}
	}
}
