package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

func setup(t *testing.T, versioning bool) (*Publisher, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:           dir,
		BaseDomain:        "example.com",
		MaxExtractBytes:   10 << 20,
		MaxExtractEntries: 100,
		VersioningEnabled: versioning,
		VersioningKeep:    2,
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(cfg, st), cfg
}

func TestPublishSingleIndex(t *testing.T) {
	p, cfg := setup(t, false)
	res, _, err := p.Publish(context.Background(), Meta{Group: "claude", Slug: "app", Title: "App"}, KindIndex, strings.NewReader("<h1>v1</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://app.claude.example.com" {
		t.Fatalf("unexpected url: %s", res.URL)
	}
	got, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "claude", "app", "index.html"))
	if string(got) != "<h1>v1</h1>" {
		t.Fatalf("content = %q", got)
	}
}

func TestPublishReplaceNoVersioning(t *testing.T) {
	p, cfg := setup(t, false)
	ctx := context.Background()
	_, _, _ = p.Publish(ctx, Meta{Slug: "demo"}, KindIndex, strings.NewReader("v1"))
	_, _, err := p.Publish(ctx, Meta{Slug: "demo"}, KindIndex, strings.NewReader("v2"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "index.html"))
	if string(got) != "v2" {
		t.Fatalf("replace failed: %q", got)
	}
	if _, err := os.Stat(cfg.VersionsDir()); !os.IsNotExist(err) {
		t.Fatal("versions dir should not exist when versioning is disabled")
	}
}

func TestVersioningAndRollback(t *testing.T) {
	p, cfg := setup(t, true)
	ctx := context.Background()
	sp, _ := router.NewSitePath("", "demo")

	for _, v := range []string{"v1", "v2", "v3"} {
		if _, _, err := p.Publish(ctx, Meta{Slug: "demo"}, KindIndex, strings.NewReader(v)); err != nil {
			t.Fatal(err)
		}
	}
	// Live should be v3; two prior versions retained (keep=2).
	live, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "index.html"))
	if string(live) != "v3" {
		t.Fatalf("live = %q", live)
	}
	versions, _ := listVersions(filepath.Join(cfg.VersionsDir(), "demo"))
	if len(versions) != 2 {
		t.Fatalf("expected 2 retained versions, got %d", len(versions))
	}

	// Rollback restores v2 (most recent archived).
	if err := p.Rollback(ctx, sp); err != nil {
		t.Fatal(err)
	}
	live, _ = os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "index.html"))
	if string(live) != "v2" {
		t.Fatalf("after rollback live = %q, want v2", live)
	}
}

func TestPublishRejectsBadPath(t *testing.T) {
	p, _ := setup(t, false)
	if _, _, err := p.Publish(context.Background(), Meta{Group: "a/b/c", Slug: "d"}, KindIndex, strings.NewReader("x")); err == nil {
		t.Fatal("expected too-deep path rejection")
	}
}
