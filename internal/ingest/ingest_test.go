package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

func setup(t *testing.T, versioning bool) (*Publisher, *config.Config) {
	t.Helper()
	p, cfg, _ := setupFull(t, 0, versioning)
	return p, cfg
}

// setupFull is like setup but also exposes the store (for DB-state assertions)
// and accepts an explicit DeletedSiteTTL.
func setupFull(t *testing.T, ttl time.Duration, versioning bool) (*Publisher, *config.Config, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:           dir,
		BaseDomain:        "example.com",
		MaxExtractBytes:   10 << 20,
		MaxExtractEntries: 100,
		VersioningEnabled: versioning,
		VersioningKeep:    2,
		DeletedSiteTTL:    ttl,
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(cfg, st), cfg, st
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
	got, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "claude", "app", "@site", "index.html"))
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
	got, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "@site", "index.html"))
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
	live, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "@site", "index.html"))
	if string(live) != "v3" {
		t.Fatalf("live = %q", live)
	}
	versions, _ := listVersions(filepath.Join(cfg.VersionsDir(), "demo", "@site"))
	if len(versions) != 2 {
		t.Fatalf("expected 2 retained versions, got %d", len(versions))
	}

	// Rollback restores v2 (most recent archived).
	if err := p.Rollback(ctx, sp); err != nil {
		t.Fatal(err)
	}
	live, _ = os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "@site", "index.html"))
	if string(live) != "v2" {
		t.Fatalf("after rollback live = %q, want v2", live)
	}
}

// TestFlatSiteAndGroupCoexist verifies a flat site and a same-named group can
// hold content simultaneously, and that re-deploying the flat site does not
// clobber the group's member sites nested beneath it.
func TestFlatSiteAndGroupCoexist(t *testing.T) {
	p, cfg := setup(t, false)
	ctx := context.Background()

	// Flat site "decks" and a member "pr-6" in the "decks" group.
	if _, _, err := p.Publish(ctx, Meta{Slug: "decks"}, KindIndex, strings.NewReader("flat-v1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Publish(ctx, Meta{Group: "decks", Slug: "pr-6"}, KindIndex, strings.NewReader("preview")); err != nil {
		t.Fatal(err)
	}

	flat := filepath.Join(cfg.SitesDir(), "decks", "@site", "index.html")
	member := filepath.Join(cfg.SitesDir(), "decks", "pr-6", "@site", "index.html")
	for _, f := range []string{flat, member} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("expected %s on disk: %v", f, err)
		}
	}

	// Re-deploy the flat site; the member must survive untouched.
	if _, _, err := p.Publish(ctx, Meta{Slug: "decks"}, KindIndex, strings.NewReader("flat-v2")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(flat); string(got) != "flat-v2" {
		t.Fatalf("flat content = %q, want flat-v2", got)
	}
	if got, _ := os.ReadFile(member); string(got) != "preview" {
		t.Fatalf("member clobbered by flat redeploy: %q", got)
	}
}

func TestPublishRejectsBadPath(t *testing.T) {
	p, _ := setup(t, false)
	if _, _, err := p.Publish(context.Background(), Meta{Group: "a/b/c", Slug: "d"}, KindIndex, strings.NewReader("x")); err == nil {
		t.Fatal("expected too-deep path rejection")
	}
}

func TestUnpublish(t *testing.T) {
	p, cfg, _ := setupFull(t, 0, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Group: "claude", Slug: "app"}, KindIndex, strings.NewReader("<h1>v1</h1>")); err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(cfg.SitesDir(), "claude", "app", "@site", "index.html")
	if _, err := os.Stat(liveFile); err != nil {
		t.Fatalf("live file missing before unpublish: %v", err)
	}

	if err := p.Unpublish(ctx, "claude", "app"); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}

	// Live file is gone; quarantine holds it.
	if _, err := os.Stat(liveFile); !os.IsNotExist(err) {
		t.Fatal("live file should be removed after unpublish")
	}
	quarantine := filepath.Join(cfg.DeletedDir(), "claude", "app", "@site", "index.html")
	if _, err := os.Stat(quarantine); err != nil {
		t.Fatalf("quarantine file missing: %v", err)
	}
}

func TestUnpublishNotFound(t *testing.T) {
	p, _, _ := setupFull(t, 0, false)
	err := p.Unpublish(context.Background(), "claude", "nonexistent")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for nonexistent site, got %v", err)
	}
}

func TestUnpublishNoFiles(t *testing.T) {
	// Unpublish should succeed even when files are missing from disk
	// (e.g. already removed externally), as long as the DB row exists.
	p, cfg, _ := setupFull(t, 0, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Slug: "bare"}, KindIndex, strings.NewReader("<h1></h1>")); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(cfg.SitesDir(), "bare"))

	if err := p.Unpublish(ctx, "", "bare"); err != nil {
		t.Fatalf("Unpublish with missing files: %v", err)
	}
}

func TestPurgeDeleted(t *testing.T) {
	p, cfg, _ := setupFull(t, 0, false) // TTL=0 → cutoff is time.Now(), purges immediately
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Group: "claude", Slug: "gone"}, KindIndex, strings.NewReader("<h1></h1>")); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpublish(ctx, "claude", "gone"); err != nil {
		t.Fatal(err)
	}
	quarantine := filepath.Join(cfg.DeletedDir(), "claude", "gone")
	if _, err := os.Stat(quarantine); err != nil {
		t.Fatalf("quarantine dir missing before purge: %v", err)
	}

	n, err := p.PurgeDeleted(ctx)
	if err != nil {
		t.Fatalf("PurgeDeleted: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 purged, got %d", n)
	}

	// Quarantine directory removed.
	if _, err := os.Stat(quarantine); !os.IsNotExist(err) {
		t.Fatal("quarantine dir should be gone after purge")
	}

	// DB row fully removed: a subsequent Unpublish returns ErrNotFound.
	if err := p.Unpublish(ctx, "claude", "gone"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound after purge, got %v", err)
	}
}

func TestPurgeDeletedRespectsRetention(t *testing.T) {
	// With a 1-hour TTL the recently-deleted site must not be purged.
	p, cfg, _ := setupFull(t, time.Hour, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Slug: "keep"}, KindIndex, strings.NewReader("<h1></h1>")); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpublish(ctx, "", "keep"); err != nil {
		t.Fatal(err)
	}

	n, err := p.PurgeDeleted(ctx)
	if err != nil {
		t.Fatalf("PurgeDeleted: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 purged within retention window, got %d", n)
	}

	// Quarantine files still present.
	if _, err := os.Stat(filepath.Join(cfg.DeletedDir(), "keep")); err != nil {
		t.Fatalf("quarantine should be retained within TTL: %v", err)
	}
}

func TestRepublishAfterUnpublish(t *testing.T) {
	p, cfg, _ := setupFull(t, 0, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Group: "claude", Slug: "site"}, KindIndex, strings.NewReader("v1")); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpublish(ctx, "claude", "site"); err != nil {
		t.Fatal(err)
	}

	// Re-publishing after unpublish serves new content.
	if _, _, err := p.Publish(ctx, Meta{Group: "claude", Slug: "site"}, KindIndex, strings.NewReader("v2")); err != nil {
		t.Fatalf("republish after unpublish: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "claude", "site", "@site", "index.html"))
	if string(got) != "v2" {
		t.Fatalf("republished content = %q, want v2", got)
	}
	// Quarantine entry persists until the next purge cycle.
	if _, err := os.Stat(filepath.Join(cfg.DeletedDir(), "claude", "site")); err != nil {
		t.Fatalf("quarantine should persist until purged: %v", err)
	}
}

func TestRestoreAfterUnpublish(t *testing.T) {
	p, cfg, _ := setupFull(t, 0, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Group: "claude", Slug: "site"}, KindIndex, strings.NewReader("v1")); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpublish(ctx, "claude", "site"); err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(cfg.SitesDir(), "claude", "site", "@site", "index.html")
	if _, err := os.Stat(liveFile); !os.IsNotExist(err) {
		t.Fatal("live file should be absent after unpublish")
	}

	if err := p.Restore(ctx, "claude", "site"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(liveFile); err != nil {
		t.Fatalf("live file should be restored: %v", err)
	}
	quarantine := filepath.Join(cfg.DeletedDir(), "claude", "site")
	if _, err := os.Stat(quarantine); !os.IsNotExist(err) {
		t.Fatal("quarantine dir should be gone after restore")
	}
}

func TestRestoreNotFound(t *testing.T) {
	p, _, _ := setupFull(t, 0, false)
	if err := p.Restore(context.Background(), "claude", "nonexistent"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPurgeOne(t *testing.T) {
	p, cfg, _ := setupFull(t, time.Hour, false)
	ctx := context.Background()

	if _, _, err := p.Publish(ctx, Meta{Group: "claude", Slug: "temp"}, KindIndex, strings.NewReader("<h1></h1>")); err != nil {
		t.Fatal(err)
	}
	if err := p.Unpublish(ctx, "claude", "temp"); err != nil {
		t.Fatal(err)
	}
	quarantine := filepath.Join(cfg.DeletedDir(), "claude", "temp")
	if _, err := os.Stat(quarantine); err != nil {
		t.Fatalf("quarantine missing before purge: %v", err)
	}

	if err := p.Purge(ctx, "claude", "temp"); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := os.Stat(quarantine); !os.IsNotExist(err) {
		t.Fatal("quarantine should be gone after Purge")
	}
	if err := p.Purge(ctx, "claude", "temp"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double-purge: want ErrNotFound, got %v", err)
	}
}

func TestPurgeLiveSiteRejected(t *testing.T) {
	p, _, _ := setupFull(t, 0, false)
	ctx := context.Background()
	if _, _, err := p.Publish(ctx, Meta{Slug: "live"}, KindIndex, strings.NewReader("<h1></h1>")); err != nil {
		t.Fatal(err)
	}
	if err := p.Purge(ctx, "", "live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("purging a live site should return ErrNotFound, got %v", err)
	}
}
