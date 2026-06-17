package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	srv, err := New(cfg, st, http.NotFoundHandler(), log)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
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

// publishMultipart builds a minimal, well-formed publish body (a meta JSON part
// plus a single-HTML index part) targeting the given group/slug, so a publish
// request reaches the capability check rather than failing to parse.
func publishMultipart(t *testing.T, group, slug string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	metaPart, err := mw.CreateFormField("meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(metaPart).Encode(map[string]any{"group": group, "slug": slug, "title": "t"}); err != nil {
		t.Fatal(err)
	}
	idx, err := mw.CreateFormField("index")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Write([]byte("<!doctype html><title>x</title><h1>hi</h1>")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// zipBundleMultipart builds a publish body whose "bundle" part is a zip archive
// with the given entries, exercising magic-byte sniffing and zip extraction.
func zipBundleMultipart(t *testing.T, group, slug string, files map[string]string) (io.Reader, string) {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	metaPart, err := mw.CreateFormField("meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(metaPart).Encode(map[string]any{"group": group, "slug": slug, "title": "t"}); err != nil {
		t.Fatal(err)
	}
	bundle, err := mw.CreateFormField("bundle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Write(zbuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// TestIngestPublishZipBundle verifies a zip bundle is sniffed, extracted, and
// served from disk — including unwrapping a single top-level directory.
func TestIngestPublishZipBundle(t *testing.T) {
	srv, st := testServer(t)
	h := srv.Handler()
	tok := mintKey(t, st, true, nil)

	body, ct := zipBundleMultipart(t, "previews", "pr-1", map[string]string{
		"app/index.html":     "<!doctype html><title>z</title>",
		"app/assets/app.css": "body{}",
	})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", body)
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("zip publish: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	for _, rel := range []string{"index.html", filepath.Join("assets", "app.css")} {
		if _, err := os.Stat(filepath.Join(srv.cfg.SitesDir(), "previews", "pr-1", rel)); err != nil {
			t.Fatalf("expected %s on disk: %v", rel, err)
		}
	}
}

// TestIngestPublishRejectsUnknownBundle ensures a "bundle" part that is neither
// gzip-tar nor zip is rejected with 400 rather than silently mis-handled.
func TestIngestPublishRejectsUnknownBundle(t *testing.T) {
	srv, st := testServer(t)
	h := srv.Handler()
	tok := mintKey(t, st, true, nil)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	metaPart, _ := mw.CreateFormField("meta")
	_ = json.NewEncoder(metaPart).Encode(map[string]any{"group": "g", "slug": "s", "title": "t"})
	bundle, _ := mw.CreateFormField("bundle")
	_, _ = bundle.Write([]byte("not an archive"))
	_ = mw.Close()

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown bundle: got %d, want 400", w.Code)
	}
}

// TestIngestRoutesRequireMatchingGrant locks the invariant that every /ingest/*
// route enforces a per-(group,slug) capability check: a valid, non-expired key
// that holds no grant matching the target must be rejected with 403, never 2xx.
// This is the structural safety net for capability enforcement living inside the
// handlers — a future route that forgets its p.Can check will fail this test.
func TestIngestRoutesRequireMatchingGrant(t *testing.T) {
	srv, st := testServer(t)
	h := srv.Handler()

	// A key with EVERY capability, but only on an unrelated group. It therefore
	// has zero grants relevant to the "mine/app" target below.
	tok := mintKey(t, st, false, []store.Grant{{
		Kind:        store.GrantGroup,
		Target:      "elsewhere",
		Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish, keys.CapRollback, keys.CapPatch},
	}})

	pubBody, pubCT := publishMultipart(t, "mine", "app")
	routes := []struct {
		name, method, target, ctype string
		body                        io.Reader
	}{
		{"publish", http.MethodPost, "http://example.com/ingest/sites", pubCT, pubBody},
		{"unpublish", http.MethodDelete, "http://example.com/ingest/sites/mine/app", "", nil},
		{"patch", http.MethodPatch, "http://example.com/ingest/sites/mine/app", "", nil},
		{"rollback", http.MethodPost, "http://example.com/ingest/sites/mine/app/rollback", "", nil},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), rt.method, rt.target, rt.body)
			if rt.ctype != "" {
				r.Header.Set("Content-Type", rt.ctype)
			}
			r.Header.Set("Authorization", "Bearer "+tok)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s with an unrelated-grant key: got %d, want 403", rt.name, w.Code)
			}
		})
	}

	// Positive control: a key scoped to the target passes the gate (proving the
	// 403s above are capability-specific, not an unrelated structural reject).
	okTok := mintKey(t, st, false, []store.Grant{{
		Kind: store.GrantGroup, Target: "mine", Permissions: []keys.Capability{keys.CapPublish},
	}})
	okBody, okCT := publishMultipart(t, "mine", "ok")
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", okBody)
	r.Header.Set("Content-Type", okCT)
	r.Header.Set("Authorization", "Bearer "+okTok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Fatal("a key scoped to the target must pass the capability gate, got 403")
	}
}

// reqStatus issues an authenticated request and returns the response status.
func reqStatus(t *testing.T, h http.Handler, method, target, token string) int {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, target, nil)
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
				r := httptest.NewRequestWithContext(context.Background(), c.method, c.target, nil)
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
