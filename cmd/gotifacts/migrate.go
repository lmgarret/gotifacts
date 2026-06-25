package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

// runMigrateLayout relocates each site's content into the reserved
// router.ContentLeaf ("@site") subdirectory under its logical path, across the
// live, versions, and quarantine trees. It is idempotent: already-migrated
// sites are left untouched, so it is safe to re-run (including after an
// interrupted run). The registry is the source of truth for which
// subdirectories are nested child sites (and must be preserved) versus the
// site's own content (which moves into @site).
func runMigrateLayout(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-layout", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "report what would move without changing anything")
	fs.BoolVar(dryRun, "n", false, "shorthand for --dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Like key management, this needs only the data dir + DB; base-domain
	// validation is irrelevant to an offline filesystem migration.
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	// Union of all registered sites (live + soft-deleted): every one may have
	// content in one or more of the trees below.
	live, err := st.ListSites(ctx, store.ListFilter{IncludeHide: true})
	if err != nil {
		return fmt.Errorf("list sites: %w", err)
	}
	deleted, err := st.ListDeletedSites(ctx)
	if err != nil {
		return fmt.Errorf("list deleted sites: %w", err)
	}

	var paths []router.SitePath
	seen := map[string]bool{}
	for _, s := range append(append([]store.Site{}, live...), deleted...) {
		sp, err := router.NewSitePath(s.Group, s.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping invalid site path group=%q slug=%q: %v\n", s.Group, s.Slug, err)
			continue
		}
		if seen[sp.Dir()] {
			continue
		}
		seen[sp.Dir()] = true
		paths = append(paths, sp)
	}

	trees := []struct {
		name string
		root string
	}{
		{"sites", cfg.SitesDir()},
		{"versions", cfg.VersionsDir()},
		{"deleted", cfg.DeletedDir()},
	}

	var moved, skipped int
	for _, sp := range paths {
		childSegs := childSegments(sp, paths)
		for _, t := range trees {
			n, err := migrateSiteDir(filepath.Join(t.root, filepath.FromSlash(sp.Dir())), childSegs, *dryRun)
			if err != nil {
				return fmt.Errorf("%s: migrate %s: %w", t.name, sp.Dir(), err)
			}
			if n > 0 {
				verb := "moved"
				if *dryRun {
					verb = "would move"
				}
				fmt.Printf("%s %d entr%s into %s/%s/%s\n", verb, n, plural(n), t.name, sp.Dir(), router.ContentLeaf)
				moved += n
			} else {
				skipped++
			}
		}
	}

	summary := "done"
	if *dryRun {
		summary = "dry run"
	}
	fmt.Printf("%s: %d entries relocated across %d site dirs\n", summary, moved, len(paths)*len(trees))
	return nil
}

// childSegments returns the set of immediate sub-directory names directly under
// sp's logical path that belong to nested child sites and must be preserved.
func childSegments(sp router.SitePath, all []router.SitePath) map[string]bool {
	prefix := sp.Dir() + "/"
	out := map[string]bool{}
	for _, other := range all {
		d := other.Dir()
		if !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := strings.TrimPrefix(d, prefix)
		seg := rest
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			seg = rest[:i]
		}
		out[seg] = true
	}
	return out
}

// migrateSiteDir moves the site's own content in dir into dir/@site, leaving the
// reserved leaf and any nested child-site directories in place. It returns the
// number of entries moved. A directory that is absent, or whose only entries are
// @site and child-site dirs, is a no-op (idempotent).
func migrateSiteDir(dir string, childSegs map[string]bool, dryRun bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var toMove []string
	for _, e := range entries {
		name := e.Name()
		if name == router.ContentLeaf || childSegs[name] {
			continue
		}
		toMove = append(toMove, name)
	}
	if len(toMove) == 0 {
		return 0, nil
	}
	sort.Strings(toMove)
	leaf := filepath.Join(dir, router.ContentLeaf)
	if !dryRun {
		if err := os.MkdirAll(leaf, 0o755); err != nil {
			return 0, err
		}
		for _, name := range toMove {
			if err := os.Rename(filepath.Join(dir, name), filepath.Join(leaf, name)); err != nil {
				return 0, err
			}
		}
	}
	return len(toMove), nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
