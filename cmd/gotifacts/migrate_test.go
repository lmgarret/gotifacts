package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lmgarret/gotifacts/internal/router"
)

func mustPath(t *testing.T, group, slug string) router.SitePath {
	t.Helper()
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		t.Fatalf("NewSitePath(%q,%q): %v", group, slug, err)
	}
	return sp
}

func TestChildSegments(t *testing.T) {
	flat := mustPath(t, "", "decks")
	member := mustPath(t, "decks", "pr-6")
	deeper := mustPath(t, "decks/pr-6", "sub")
	all := []router.SitePath{flat, member, deeper}

	cs := childSegments(flat, all)
	if len(cs) != 1 || !cs["pr-6"] {
		t.Fatalf("childSegments(decks) = %v, want {pr-6}", cs)
	}
	cs = childSegments(member, all)
	if len(cs) != 1 || !cs["sub"] {
		t.Fatalf("childSegments(decks/pr-6) = %v, want {sub}", cs)
	}
	if cs := childSegments(deeper, all); len(cs) != 0 {
		t.Fatalf("childSegments(leaf) = %v, want empty", cs)
	}
}

func TestMigrateSiteDir(t *testing.T) {
	root := t.TempDir()
	// Legacy layout: flat "decks" content directly in the dir, plus a nested
	// child site "pr-6" that must be preserved in place.
	dir := filepath.Join(root, "decks")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("flat"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.css"), []byte("css"), 0o644); err != nil {
		t.Fatal(err)
	}
	childDir := filepath.Join(dir, "pr-6", "@site")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "index.html"), []byte("preview"), 0o644); err != nil {
		t.Fatal(err)
	}

	childSegs := map[string]bool{"pr-6": true}
	n, err := migrateSiteDir(dir, childSegs, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 { // index.html + assets/
		t.Fatalf("moved %d entries, want 2", n)
	}

	// Flat content relocated into @site.
	if got, _ := os.ReadFile(filepath.Join(dir, "@site", "index.html")); string(got) != "flat" {
		t.Fatalf("flat index not relocated: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "@site", "assets", "app.css")); string(got) != "css" {
		t.Fatalf("flat asset not relocated: %q", got)
	}
	// Child site preserved untouched.
	if got, _ := os.ReadFile(filepath.Join(childDir, "index.html")); string(got) != "preview" {
		t.Fatalf("child site disturbed: %q", got)
	}
	// Old top-level files are gone.
	if _, err := os.Stat(filepath.Join(dir, "index.html")); !os.IsNotExist(err) {
		t.Fatal("legacy top-level index.html should be gone")
	}

	// Idempotent: a second run moves nothing.
	if n, err := migrateSiteDir(dir, childSegs, false); err != nil || n != 0 {
		t.Fatalf("re-run migrated %d entries (err=%v), want 0", n, err)
	}
}

func TestMigrateSiteDirAbsentIsNoop(t *testing.T) {
	n, err := migrateSiteDir(filepath.Join(t.TempDir(), "nope"), nil, false)
	if err != nil || n != 0 {
		t.Fatalf("absent dir: n=%d err=%v", n, err)
	}
}
