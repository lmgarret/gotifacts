package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/keys"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSiteUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	s, err := st.UpsertSite(ctx, "claude", "app", SiteInput{Title: "App", Tags: []string{"go", "go", "web"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID == 0 || s.Title != "App" {
		t.Fatalf("unexpected site: %+v", s)
	}
	if len(s.Tags) != 2 { // duplicate "go" deduped
		t.Fatalf("tags not normalized: %v", s.Tags)
	}

	// Idempotent replace preserves created_at, updates title.
	s2, err := st.UpsertSite(ctx, "claude", "app", SiteInput{Title: "App v2"})
	if err != nil {
		t.Fatal(err)
	}
	if s2.ID != s.ID {
		t.Fatalf("upsert created a new row: %d != %d", s2.ID, s.ID)
	}
	if !s2.CreatedAt.Equal(s.CreatedAt) {
		t.Fatal("created_at not preserved on update")
	}
	if s2.Title != "App v2" {
		t.Fatalf("title not updated: %q", s2.Title)
	}
}

func TestSiteSizePersistAndSet(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	s, err := st.UpsertSite(ctx, "claude", "app", SiteInput{Title: "App", Size: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if s.Size != 4096 {
		t.Fatalf("size not persisted on upsert: %d", s.Size)
	}

	got, err := st.GetSite(ctx, "claude", "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 4096 {
		t.Fatalf("size not loaded on get: %d", got.Size)
	}

	if err := st.SetSiteSize(ctx, "claude", "app", 8192); err != nil {
		t.Fatalf("SetSiteSize: %v", err)
	}
	got, err = st.GetSite(ctx, "claude", "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 8192 {
		t.Fatalf("SetSiteSize did not update: %d", got.Size)
	}
}

func TestSiteDeleteAndNotFound(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.GetSite(ctx, "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := st.DeleteSite(ctx, "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	_, _ = st.UpsertSite(ctx, "", "demo", SiteInput{})
	if err := st.DeleteSite(ctx, "", "demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestListSitesFilters(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "claude", "alpha", SiteInput{Title: "Alpha", Tags: []string{"demo"}, Date: "2026-01-01T00:00:00Z"})
	_, _ = st.UpsertSite(ctx, "claude", "beta", SiteInput{Title: "Beta", Tags: []string{"prod"}, Date: "2026-02-01T00:00:00Z"})
	_, _ = st.UpsertSite(ctx, "", "hidden", SiteInput{Title: "Secret", Hidden: true})

	// Hidden excluded by default.
	all, err := st.ListSites(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 visible sites, got %d", len(all))
	}
	// Default sort is date desc → beta first.
	if all[0].Slug != "beta" {
		t.Fatalf("date sort wrong: %s first", all[0].Slug)
	}

	// Tag filter.
	tagged, _ := st.ListSites(ctx, ListFilter{Tag: "prod"})
	if len(tagged) != 1 || tagged[0].Slug != "beta" {
		t.Fatalf("tag filter failed: %+v", tagged)
	}

	// Group filter.
	grp, _ := st.ListSites(ctx, ListFilter{Group: "claude"})
	if len(grp) != 2 {
		t.Fatalf("group filter failed: %d", len(grp))
	}

	// Include hidden.
	withHidden, _ := st.ListSites(ctx, ListFilter{IncludeHide: true})
	if len(withHidden) != 3 {
		t.Fatalf("expected 3 with hidden, got %d", len(withHidden))
	}
}

// TestLegacyKeyMigration verifies that keys created under the old
// scope/group_restriction schema keep their exact access after migration 0002:
// admin keys become superusers, publish keys get an equivalent publish grant.
func TestLegacyKeyMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	_, pubHash, _ := keys.Generate()
	_, adminHash, _ := keys.Generate()

	// Seed a database at the pre-0002 schema with two legacy keys, and mark only
	// migration 0001 as applied so Open() runs 0002's backfill against them.
	func() {
		db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		body, err := os.ReadFile("migrations/0001_init.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply 0001: %v", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations VALUES ('migrations/0001_init.sql', ?)`, now()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO api_keys (name, key_hash, scope, group_restriction, created_at) VALUES (?,?,?,?,?)`,
			"old-publish", pubHash, "publish", "claude", now()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO api_keys (name, key_hash, scope, group_restriction, created_at) VALUES (?,?,?,?,?)`,
			"old-admin", adminHash, "admin", nil, now()); err != nil {
			t.Fatal(err)
		}
	}()

	// Open through the store: migration 0002 should now run and backfill grants.
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	defer func() { _ = st.Close() }()

	pub, err := st.FindKeyByHash(ctx, pubHash)
	if err != nil {
		t.Fatalf("legacy publish key not found post-migration: %v", err)
	}
	if pub.Admin {
		t.Fatal("legacy publish key must not become admin")
	}
	if len(pub.Grants) != 1 || pub.Grants[0].Kind != GrantGroup || pub.Grants[0].Target != "claude" ||
		!keys.HasCapability(pub.Grants[0].Permissions, keys.CapPublish) {
		t.Fatalf("legacy publish key got wrong grants: %+v", pub.Grants)
	}

	adm, err := st.FindKeyByHash(ctx, adminHash)
	if err != nil {
		t.Fatalf("legacy admin key not found post-migration: %v", err)
	}
	if !adm.Admin || len(adm.Grants) != 0 {
		t.Fatalf("legacy admin key should be a grant-less superuser: %+v", adm)
	}
}

// TestLoadGrantsBadPermissions verifies that a grant row with an unparseable
// permissions value loads fail-closed (no capabilities) and is logged, rather
// than failing the whole key lookup or silently denying.
func TestLoadGrantsBadPermissions(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	var logbuf bytes.Buffer
	st.SetLogger(slog.New(slog.NewTextHandler(&logbuf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	_, hash, _ := keys.Generate()
	rec, err := st.CreateKey(ctx, "k", false,
		[]Grant{{Kind: GrantGroup, Target: "docs", Permissions: []keys.Capability{keys.CapPublish}}}, nil, hash)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the stored permissions to a value this binary cannot parse.
	if _, err := st.db.ExecContext(ctx,
		`UPDATE api_key_grants SET permissions=? WHERE key_id=?`, "totally-bogus", rec.ID); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetKey(ctx, rec.ID)
	if err != nil {
		t.Fatalf("key should still load despite a bad grant: %v", err)
	}
	if len(got.Grants) != 1 {
		t.Fatalf("expected the grant to remain, got %+v", got.Grants)
	}
	if len(got.Grants[0].Permissions) != 0 {
		t.Fatalf("bad grant must convey no capability, got %v", got.Grants[0].Permissions)
	}
	// The failure must be observable with enough context to diagnose.
	logs := logbuf.String()
	if !strings.Contains(logs, "key_id") || !strings.Contains(logs, "totally-bogus") {
		t.Fatalf("expected a warning mentioning key_id and the offending value, got: %q", logs)
	}
}

func TestSoftDelete(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_, _ = st.UpsertSite(ctx, "claude", "app", SiteInput{Title: "App"})

	// Site is visible before deletion.
	if _, err := st.GetSite(ctx, "claude", "app"); err != nil {
		t.Fatalf("GetSite before soft-delete: %v", err)
	}

	if err := st.SoftDeleteSite(ctx, "claude", "app"); err != nil {
		t.Fatalf("SoftDeleteSite: %v", err)
	}

	// GetSite returns ErrNotFound for soft-deleted sites.
	if _, err := st.GetSite(ctx, "claude", "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after soft-delete, got %v", err)
	}

	// ListSites never returns soft-deleted sites, even with IncludeHide.
	sites, _ := st.ListSites(ctx, ListFilter{IncludeHide: true})
	for _, s := range sites {
		if s.Group == "claude" && s.Slug == "app" {
			t.Fatal("soft-deleted site must not appear in ListSites")
		}
	}
}

func TestSoftDeleteIdempotency(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "claude", "app", SiteInput{})

	if err := st.SoftDeleteSite(ctx, "claude", "app"); err != nil {
		t.Fatalf("first SoftDeleteSite: %v", err)
	}
	// Deleting an already-soft-deleted site returns ErrNotFound.
	if err := st.SoftDeleteSite(ctx, "claude", "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second SoftDeleteSite: want ErrNotFound, got %v", err)
	}
	// Deleting a nonexistent site also returns ErrNotFound.
	if err := st.SoftDeleteSite(ctx, "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonexistent SoftDeleteSite: want ErrNotFound, got %v", err)
	}
}

func TestListDeletedBefore(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "", "a", SiteInput{})
	_, _ = st.UpsertSite(ctx, "", "b", SiteInput{})
	_ = st.SoftDeleteSite(ctx, "", "a")

	// Future cutoff includes the deleted site.
	deleted, err := st.ListDeletedBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0].Slug != "a" {
		t.Fatalf("expected [a], got %+v", deleted)
	}
	if deleted[0].DeletedAt == nil {
		t.Fatal("DeletedAt should be non-nil on a soft-deleted site")
	}

	// Past cutoff returns nothing.
	none, _ := st.ListDeletedBefore(ctx, time.Now().Add(-time.Hour))
	if len(none) != 0 {
		t.Fatalf("expected empty with past cutoff, got %+v", none)
	}

	// Live site "b" is never included.
	for _, s := range deleted {
		if s.Slug == "b" {
			t.Fatal("live site must not appear in ListDeletedBefore")
		}
	}
}

func TestUpsertRestoresSoftDeleted(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "claude", "app", SiteInput{Title: "v1"})
	_ = st.SoftDeleteSite(ctx, "claude", "app")

	// Re-upserting the same slug clears deleted_at and updates the title.
	s, err := st.UpsertSite(ctx, "claude", "app", SiteInput{Title: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if s.DeletedAt != nil {
		t.Fatalf("deleted_at should be nil after re-upsert, got %v", s.DeletedAt)
	}
	if s.Title != "v2" {
		t.Fatalf("title not updated: %q", s.Title)
	}

	// GetSite finds it again.
	if _, err := st.GetSite(ctx, "claude", "app"); err != nil {
		t.Fatalf("GetSite after re-upsert: %v", err)
	}
}

func TestUpsertTouchRestoresSoftDeleted(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "", "demo", SiteInput{})
	_ = st.SoftDeleteSite(ctx, "", "demo")

	// UpsertSiteTouch (called by rollback) clears deleted_at.
	if _, err := st.UpsertSiteTouch(ctx, "", "demo"); err != nil {
		t.Fatalf("UpsertSiteTouch: %v", err)
	}
	if _, err := st.GetSite(ctx, "", "demo"); err != nil {
		t.Fatalf("GetSite after UpsertSiteTouch: %v", err)
	}
}

func TestListDeletedSites(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "claude", "live", SiteInput{Title: "Live"})
	_, _ = st.UpsertSite(ctx, "claude", "gone", SiteInput{Title: "Gone"})
	_ = st.SoftDeleteSite(ctx, "claude", "gone")

	deleted, err := st.ListDeletedSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0].Slug != "gone" {
		t.Fatalf("expected [gone], got %+v", deleted)
	}
	// Live site must not appear.
	for _, s := range deleted {
		if s.Slug == "live" {
			t.Fatal("live site must not appear in ListDeletedSites")
		}
	}
}

func TestRestoreSite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "", "app", SiteInput{Title: "App"})
	_ = st.SoftDeleteSite(ctx, "", "app")

	if err := st.RestoreSite(ctx, "", "app"); err != nil {
		t.Fatalf("RestoreSite: %v", err)
	}
	// Site is visible again.
	if _, err := st.GetSite(ctx, "", "app"); err != nil {
		t.Fatalf("GetSite after restore: %v", err)
	}

	// Restoring a live site returns ErrNotFound (not currently soft-deleted).
	if err := st.RestoreSite(ctx, "", "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore live site: want ErrNotFound, got %v", err)
	}
	// Restoring a nonexistent site also returns ErrNotFound.
	if err := st.RestoreSite(ctx, "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore missing site: want ErrNotFound, got %v", err)
	}
}

func TestPurgeDeletedSite(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, _ = st.UpsertSite(ctx, "", "app", SiteInput{})
	_ = st.SoftDeleteSite(ctx, "", "app")

	if err := st.PurgeDeletedSite(ctx, "", "app"); err != nil {
		t.Fatalf("PurgeDeletedSite: %v", err)
	}
	// Row fully gone.
	if err := st.PurgeDeletedSite(ctx, "", "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double-purge: want ErrNotFound, got %v", err)
	}

	// Purging a live (non-deleted) site must fail with ErrNotFound.
	_, _ = st.UpsertSite(ctx, "", "live", SiteInput{})
	if err := st.PurgeDeletedSite(ctx, "", "live"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge live site: want ErrNotFound, got %v", err)
	}
}

func TestKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	tok, hash, _ := keys.Generate()
	grants := []Grant{
		{Kind: GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish}},
		{Kind: GrantSite, Target: "docs/app", Permissions: []keys.Capability{keys.CapPatch}},
	}
	rec, err := st.CreateKey(ctx, "ci", false, grants, nil, hash)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Admin {
		t.Fatalf("scoped key must not be admin: %+v", rec)
	}
	if len(rec.Grants) != 2 || rec.Grants[0].Target != "claude" || rec.Grants[0].Kind != GrantGroup {
		t.Fatalf("unexpected grants: %+v", rec.Grants)
	}
	if rec.Grants[1].Kind != GrantSite || rec.Grants[1].Target != "docs/app" {
		t.Fatalf("site grant not stored: %+v", rec.Grants[1])
	}

	found, err := st.FindKeyByHash(ctx, keys.Hash(tok))
	if err != nil {
		t.Fatalf("find by hash: %v", err)
	}
	if found.ID != rec.ID {
		t.Fatal("hash lookup returned wrong key")
	}
	if len(found.Grants) != 2 || found.Grants[0].Target != "claude" {
		t.Fatalf("grants not loaded on lookup: %+v", found.Grants)
	}

	if _, err := st.FindKeyByHash(ctx, keys.Hash("gtf_bogus")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bogus lookup: want ErrNotFound, got %v", err)
	}

	if err := st.DeleteKey(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetKey(ctx, rec.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}
