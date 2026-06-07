package store

import (
	"context"
	"errors"
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

func TestKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	tok, hash, _ := keys.Generate()
	rec, err := st.CreateKey(ctx, "ci", keys.ScopePublish, "claude", hash)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Scope != keys.ScopePublish || rec.GroupRestriction != "claude" {
		t.Fatalf("unexpected key: %+v", rec)
	}

	found, err := st.FindKeyByHash(ctx, keys.Hash(tok))
	if err != nil {
		t.Fatalf("find by hash: %v", err)
	}
	if found.ID != rec.ID {
		t.Fatal("hash lookup returned wrong key")
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
