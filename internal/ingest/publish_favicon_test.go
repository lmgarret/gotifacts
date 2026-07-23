package ingest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/lmgarret/gotifacts/internal/archive"
	"github.com/lmgarret/gotifacts/internal/store"
)

func bundle(t *testing.T, files ...archive.NamedFile) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := archive.WriteTarGz(&buf, files); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestPublishDetectsFavicon(t *testing.T) {
	p, _, st := setupFull(t, 0, false)
	ctx := context.Background()

	buf := bundle(t,
		archive.NamedFile{Name: "index.html", Data: []byte(`<link rel="icon" href="/icon.svg">`)},
		archive.NamedFile{Name: "icon.svg", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
	)
	_, site, err := p.Publish(ctx, Meta{Slug: "demo"}, KindBundle, buf)
	if err != nil {
		t.Fatal(err)
	}
	if !site.HasFavicon {
		t.Fatal("returned site should report HasFavicon")
	}

	// The registry row reflects it, and the bytes round-trip.
	got, err := st.GetSite(ctx, "", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasFavicon {
		t.Fatal("stored site should report HasFavicon")
	}
	data, ct, err := st.GetSiteFavicon(ctx, "", "demo")
	if err != nil {
		t.Fatalf("GetSiteFavicon: %v", err)
	}
	if string(data) != `<svg xmlns="http://www.w3.org/2000/svg"/>` || ct != "image/svg+xml" {
		t.Fatalf("favicon = (%q, %q)", data, ct)
	}
}

func TestRepublishClearsFavicon(t *testing.T) {
	p, _, st := setupFull(t, 0, false)
	ctx := context.Background()

	// First publish declares an icon.
	if _, _, err := p.Publish(ctx, Meta{Slug: "demo"}, KindBundle, bundle(t,
		archive.NamedFile{Name: "index.html", Data: []byte(`<link rel="icon" href="/icon.png">`)},
		archive.NamedFile{Name: "icon.png", Data: []byte("PNG")},
	)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSiteFavicon(ctx, "", "demo"); err != nil {
		t.Fatalf("expected favicon after first publish: %v", err)
	}

	// Re-publishing without an icon clears the cached favicon.
	if _, _, err := p.Publish(ctx, Meta{Slug: "demo"}, KindIndex, bytes.NewReader([]byte("<h1>no icon</h1>"))); err != nil {
		t.Fatal(err)
	}
	site, err := st.GetSite(ctx, "", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if site.HasFavicon {
		t.Fatal("HasFavicon should be false after republishing without an icon")
	}
	if _, _, err := st.GetSiteFavicon(ctx, "", "demo"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound after clearing, got %v", err)
	}
}

func TestBackfillFavicons(t *testing.T) {
	p, _, st := setupFull(t, 0, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Slug: "demo"}, KindBundle, bundle(t,
		archive.NamedFile{Name: "index.html", Data: []byte(`<link rel="icon" href="/icon.png">`)},
		archive.NamedFile{Name: "icon.png", Data: []byte("PNG")},
	)); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-feature row by clearing the cached favicon.
	if err := st.SetSiteFavicon(ctx, "", "demo", nil, ""); err != nil {
		t.Fatal(err)
	}

	// Dry-run reports the change without writing.
	scanned, updated, err := p.BackfillFavicons(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 1 || updated != 1 {
		t.Fatalf("dry-run: scanned=%d updated=%d, want 1/1", scanned, updated)
	}
	if _, _, err := st.GetSiteFavicon(ctx, "", "demo"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("dry-run must not write")
	}

	// Real run populates it, and a second run is a no-op.
	if _, updated, err = p.BackfillFavicons(ctx, false); err != nil || updated != 1 {
		t.Fatalf("backfill: updated=%d err=%v, want 1/nil", updated, err)
	}
	if data, _, err := st.GetSiteFavicon(ctx, "", "demo"); err != nil || string(data) != "PNG" {
		t.Fatalf("after backfill favicon = %q err=%v", data, err)
	}
	if _, updated, err = p.BackfillFavicons(ctx, false); err != nil || updated != 0 {
		t.Fatalf("second backfill should be a no-op: updated=%d err=%v", updated, err)
	}
}
