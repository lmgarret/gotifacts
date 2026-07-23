package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/lmgarret/gotifacts/internal/archive"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/store"
)

func TestServeFavicon(t *testing.T) {
	srv, ts := filesTestServer(t)
	ctx := context.Background()

	var buf bytes.Buffer
	if err := archive.WriteTarGz(&buf, []archive.NamedFile{
		{Name: "index.html", Data: []byte(`<link rel="icon" href="/icon.png">`)},
		{Name: "icon.png", Data: []byte("PNGBYTES")},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.pub.Publish(ctx, ingest.Meta{Slug: "fav", Title: "Fav"}, ingest.KindBundle, &buf); err != nil {
		t.Fatal(err)
	}

	// The cached favicon streams back with its detected content-type.
	resp := doGet(t, ts, "/api/sites/fav/favicon", "bob")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "PNGBYTES" {
		t.Fatalf("favicon = %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}

	// A site without a favicon 404s.
	if resp := doGet(t, ts, "/api/sites/demo/favicon", "bob"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no-favicon site status = %d, want 404", resp.StatusCode)
	}

	// A hidden site's favicon is invisible to viewers but visible to admins.
	hidden := true
	if _, err := srv.store.PatchSite(ctx, "", "fav", store.SitePatch{Hidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	if resp := doGet(t, ts, "/api/sites/fav/favicon", "bob"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("hidden favicon as viewer = %d, want 404", resp.StatusCode)
	}
	if resp := doGet(t, ts, "/api/sites/fav/favicon", "alice"); resp.StatusCode != http.StatusOK {
		t.Fatalf("hidden favicon as admin = %d, want 200", resp.StatusCode)
	}
}
