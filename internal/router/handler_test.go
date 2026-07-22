package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
)

func TestDispatchByHost(t *testing.T) {
	cfg := &config.Config{BaseDomain: "example.com", AliasDomains: []string{"example.org"}}
	mark := func(plane string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(plane))
		})
	}
	d := NewDispatch(cfg, mark("apex"), mark("sites"))

	tests := []struct {
		host string
		want string
	}{
		{"example.com", "apex"},      // canonical apex
		{"example.org", "apex"},      // alias apex
		{"app.example.com", "sites"}, // canonical sub-domain -> site plane
		{"app.example.org", "sites"}, // alias sub-domain -> site plane
		{"unrelated.net", "sites"},   // unknown host -> site plane (404s downstream)
		{"example.org:8080", "apex"}, // apex with port
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+tt.host+"/", nil)
			r.Host = tt.host
			w := httptest.NewRecorder()
			d.ServeHTTP(w, r)
			if got := w.Body.String(); got != tt.want {
				t.Fatalf("host %q dispatched to %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}
