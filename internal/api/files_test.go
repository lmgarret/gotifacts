package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

// filesTestServer builds an API server with versioning enabled and a single
// published site ("demo") that has one archived version plus current.
func filesTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("GOTIFACTS_BASE_DOMAIN", "example.com")
	t.Setenv("GOTIFACTS_ADMIN_USERS", "alice")
	t.Setenv("GOTIFACTS_TRUSTED_PROXIES", "127.0.0.1/32")
	t.Setenv("GOTIFACTS_DATA_DIR", t.TempDir())
	t.Setenv("GOTIFACTS_VERSIONING_ENABLED", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir()+"/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv, err := New(cfg, st, http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, v := range []string{"<h1>v1</h1>", "<h1>v2</h1>"} {
		if _, _, err := srv.pub.Publish(ctx, ingest.Meta{Slug: "demo", Title: "Demo"}, ingest.KindIndex, strings.NewReader(v)); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// doGet issues a GET with an optional forward-auth user (trusted via 127.0.0.1).
func doGet(t *testing.T, ts *httptest.Server, path, user string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
	if user != "" {
		req.Header.Set("Remote-User", user)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRevisionsAndFilesAsViewer(t *testing.T) {
	_, ts := filesTestServer(t)

	// Viewer (non-admin) may list revisions: current + one archived.
	resp := doGet(t, ts, "/api/sites/demo/revisions", "bob")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revisions status = %d", resp.StatusCode)
	}
	var rl struct {
		Revisions []ingest.Revision `json:"revisions"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&rl)
	_ = resp.Body.Close()
	if len(rl.Revisions) != 2 || !rl.Revisions[0].Current {
		t.Fatalf("unexpected revisions: %+v", rl.Revisions)
	}

	// File tree for current contains index.html.
	resp = doGet(t, ts, "/api/sites/demo/revisions/current/files", "bob")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("files status = %d", resp.StatusCode)
	}
	var tree FileNode
	_ = json.NewDecoder(resp.Body).Decode(&tree)
	_ = resp.Body.Close()
	if len(tree.Children) != 1 || tree.Children[0].Name != "index.html" || tree.Children[0].Dir {
		t.Fatalf("unexpected file tree: %+v", tree)
	}

	// Single-file download returns content with an attachment disposition.
	resp = doGet(t, ts, "/api/sites/demo/revisions/current/file?path=index.html", "bob")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "<h1>v2</h1>" {
		t.Fatalf("file download = %d %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("missing attachment disposition: %q", cd)
	}

	// Archive download is a zip.
	resp = doGet(t, ts, "/api/sites/demo/revisions/current/archive", "bob")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("archive = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	_ = resp.Body.Close()
}

func TestBrowseTraversalAndUnknownRevision(t *testing.T) {
	_, ts := filesTestServer(t)

	// Path traversal in the file param is rejected.
	resp := doGet(t, ts, "/api/sites/demo/revisions/current/file?path=../../etc/passwd", "bob")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("traversal status = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Unknown revision id is rejected.
	resp = doGet(t, ts, "/api/sites/demo/revisions/bogus/files", "bob")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown revision status = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestHiddenSiteInvisibleToViewer(t *testing.T) {
	srv, ts := filesTestServer(t)
	hidden := true
	if _, err := srv.store.PatchSite(context.Background(), "", "demo", store.SitePatch{Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}

	// Viewer cannot browse a hidden site.
	resp := doGet(t, ts, "/api/sites/demo/revisions", "bob")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("hidden site viewer status = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Admin still can.
	resp = doGet(t, ts, "/api/sites/demo/revisions", "alice")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hidden site admin status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestRollbackToRevisionAdminOnly(t *testing.T) {
	srv, ts := filesTestServer(t)
	ctx := context.Background()
	sp, _ := router.NewSitePath("", "demo")
	revs, err := srv.pub.ListRevisions(sp)
	if err != nil {
		t.Fatal(err)
	}
	archived := revs[1].ID // the archived (v1) revision

	// Viewer cannot roll back (admin-gated route).
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/sites/demo/rollback",
		strings.NewReader(`{"revision":"`+archived+`"}`))
	req.Header.Set("Remote-User", "bob")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer rollback status = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Admin rolls back to the archived revision; live content becomes v1.
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/sites/demo/rollback",
		strings.NewReader(`{"revision":"`+archived+`"}`))
	req.Header.Set("Remote-User", "alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin rollback status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	got := doGet(t, ts, "/api/sites/demo/revisions/current/file?path=index.html", "alice")
	body, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if string(body) != "<h1>v1</h1>" {
		t.Fatalf("after rollback live = %q, want v1", body)
	}
}
