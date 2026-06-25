package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmgarret/gotifacts/internal/router"
)

// publishSeq publishes a sequence of single-index versions for the demo site.
func publishSeq(t *testing.T, p *Publisher, versions ...string) {
	t.Helper()
	for _, v := range versions {
		if _, _, err := p.Publish(context.Background(), Meta{Slug: "demo"}, KindIndex, strings.NewReader(v)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListRevisions(t *testing.T) {
	p, _ := setup(t, true)
	sp, _ := router.NewSitePath("", "demo")
	publishSeq(t, p, "v1", "v2", "v3") // keep=2, so v1 pruned

	revs, err := p.ListRevisions(sp)
	if err != nil {
		t.Fatal(err)
	}
	// current (v3) + 2 archived (v2, v1's slot pruned leaving v1,v2 -> keep newest 2)
	if len(revs) != 3 {
		t.Fatalf("expected 3 revisions, got %d: %+v", len(revs), revs)
	}
	if !revs[0].Current || revs[0].ID != CurrentRevision {
		t.Fatalf("first revision should be current, got %+v", revs[0])
	}
	// Archived revisions are newest-first and not marked current.
	if revs[1].Current || revs[2].Current {
		t.Fatal("archived revisions should not be marked current")
	}
	if revs[1].ID <= revs[2].ID {
		t.Fatalf("archived revisions not newest-first: %s then %s", revs[1].ID, revs[2].ID)
	}
}

func TestRevisionDirRejectsTraversalAndUnknown(t *testing.T) {
	p, cfg := setup(t, true)
	sp, _ := router.NewSitePath("", "demo")
	publishSeq(t, p, "v1", "v2")

	// current resolves to the live dir.
	dir, err := p.RevisionDir(sp, CurrentRevision)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(cfg.SitesDir(), "demo", "@site") {
		t.Fatalf("current dir = %s", dir)
	}

	// Unknown ids and traversal attempts are rejected, not resolved.
	for _, bad := range []string{"nope", "../demo", "..", "../../etc"} {
		if _, err := p.RevisionDir(sp, bad); err == nil {
			t.Fatalf("expected error for revision %q", bad)
		}
	}
}

func TestRollbackToPromotesAndKeepsRevision(t *testing.T) {
	p, cfg := setup(t, true)
	ctx := context.Background()
	sp, _ := router.NewSitePath("", "demo")
	publishSeq(t, p, "v1", "v2", "v3") // keep=2 -> archived holds v1,v2; live v3

	revs, err := p.ListRevisions(sp)
	if err != nil {
		t.Fatal(err)
	}
	// Pick the newest archived revision (revs[0] is current). Archiving the
	// current live during rollback may prune the oldest, so target one that
	// survives to assert copy-not-move semantics.
	target := revs[1]
	srcContent, _ := os.ReadFile(filepath.Join(cfg.VersionsDir(), "demo", "@site", target.ID, "index.html"))

	if err := p.RollbackTo(ctx, sp, target.ID); err != nil {
		t.Fatal(err)
	}

	// Live now matches the promoted revision.
	live, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "@site", "index.html"))
	if string(live) != string(srcContent) {
		t.Fatalf("live = %q, want %q", live, srcContent)
	}
	// The promoted revision still exists in history (copy, not move).
	if _, err := os.Stat(filepath.Join(cfg.VersionsDir(), "demo", "@site", target.ID)); err != nil {
		t.Fatalf("promoted revision should remain in history: %v", err)
	}
}

func TestRollbackToCurrentIsNoop(t *testing.T) {
	p, cfg := setup(t, true)
	ctx := context.Background()
	sp, _ := router.NewSitePath("", "demo")
	publishSeq(t, p, "v1", "v2")

	before, _ := listVersions(filepath.Join(cfg.VersionsDir(), "demo", "@site"))
	if err := p.RollbackTo(ctx, sp, CurrentRevision); err != nil {
		t.Fatal(err)
	}
	after, _ := listVersions(filepath.Join(cfg.VersionsDir(), "demo", "@site"))
	if len(after) != len(before) {
		t.Fatalf("no-op rollback changed history: %d -> %d", len(before), len(after))
	}
	live, _ := os.ReadFile(filepath.Join(cfg.SitesDir(), "demo", "@site", "index.html"))
	if string(live) != "v2" {
		t.Fatalf("live changed by no-op rollback: %q", live)
	}
}
