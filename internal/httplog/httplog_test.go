package httplog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestLogger returns a JSON logger writing to buf at debug level so every
// record is captured regardless of the success level under test.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestMiddlewareLevelByStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		success   slog.Level
		wantLevel string
	}{
		{"ok at info", http.StatusOK, slog.LevelInfo, "INFO"},
		{"ok at debug", http.StatusOK, slog.LevelDebug, "DEBUG"},
		{"client error", http.StatusNotFound, slog.LevelInfo, "WARN"},
		{"server error", http.StatusInternalServerError, slog.LevelInfo, "ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := Middleware(newTestLogger(&buf), tc.success)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("body"))
			}))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://app.example.com/x", nil)
			h.ServeHTTP(httptest.NewRecorder(), req)

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("log not emitted/parseable: %q: %v", buf.String(), err)
			}
			if rec["level"] != tc.wantLevel {
				t.Errorf("level = %v, want %v", rec["level"], tc.wantLevel)
			}
			if rec["status"] != float64(tc.status) {
				t.Errorf("status = %v, want %d", rec["status"], tc.status)
			}
			if rec["bytes"] != float64(len("body")) {
				t.Errorf("bytes = %v, want %d", rec["bytes"], len("body"))
			}
			if rec["method"] != http.MethodGet || rec["path"] != "/x" {
				t.Errorf("unexpected method/path: %v %v", rec["method"], rec["path"])
			}
		})
	}
}

// TestMiddlewareDefaultStatus verifies a handler that never calls WriteHeader is
// logged as 200 (the implicit status).
func TestMiddlewareDefaultStatus(t *testing.T) {
	var buf bytes.Buffer
	h := Middleware(newTestLogger(&buf), slog.LevelInfo)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://h/", nil))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log not parseable: %v", err)
	}
	if rec["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", rec["status"])
	}
}
