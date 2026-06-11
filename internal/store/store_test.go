package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

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

func TestKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	tok, hash, _ := keys.Generate()
	grants := []Grant{
		{Kind: GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish}},
		{Kind: GrantSite, Target: "docs/app", Permissions: []keys.Capability{keys.CapPatch}},
	}
	rec, err := st.CreateKey(ctx, "ci", false, grants, hash)
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
