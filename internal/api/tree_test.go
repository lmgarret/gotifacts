package api

import (
	"testing"

	"github.com/lmgarret/gotifacts/internal/store"
)

func TestBuildTreeMergesFlatSiteIntoGroup(t *testing.T) {
	sites := []store.Site{
		{Group: "", Slug: "decks", Title: "Decks landing"},
		{Group: "decks", Slug: "pr-6", Title: "PR 6"},
		{Group: "decks", Slug: "pr-7", Title: "PR 7"},
		{Group: "", Slug: "standalone", Title: "Standalone"},
	}
	root := BuildTree(sites)

	// The flat "decks" site is absorbed as the group's landing site, not a
	// sibling card at the root.
	for _, s := range root.Sites {
		if s.Group == "" && s.Slug == "decks" {
			t.Fatal("flat decks site should not remain in root.Sites")
		}
	}
	if len(root.Groups) != 1 || root.Groups[0].Path != "decks" {
		t.Fatalf("expected a single 'decks' group, got %+v", root.Groups)
	}
	g := root.Groups[0]
	if g.Site == nil || g.Site.Slug != "decks" {
		t.Fatalf("decks group should carry the flat site as its landing node, got %+v", g.Site)
	}
	if len(g.Sites) != 2 {
		t.Fatalf("decks group should have 2 member sites, got %d", len(g.Sites))
	}
	// Unrelated flat sites are untouched.
	var foundStandalone bool
	for _, s := range root.Sites {
		if s.Slug == "standalone" {
			foundStandalone = true
		}
	}
	if !foundStandalone {
		t.Fatal("standalone flat site missing from root.Sites")
	}
}

func TestBuildTreeNoMergeWithoutGroup(t *testing.T) {
	// A flat site with no same-named group stays a root card.
	root := BuildTree([]store.Site{{Group: "", Slug: "decks"}})
	if len(root.Sites) != 1 || root.Sites[0].Slug != "decks" {
		t.Fatalf("flat site without a group should remain in root.Sites, got %+v", root.Sites)
	}
	if len(root.Groups) != 0 {
		t.Fatalf("no groups expected, got %+v", root.Groups)
	}
}
