package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/store"
)

// runBackfillFavicons re-detects and caches the favicon for every live site,
// reading each site's published content. It is idempotent (unchanged favicons
// are left untouched), so it is safe to re-run, and supports --dry-run to
// preview what would change. Use it to populate favicons for sites published
// before favicon support existed.
func runBackfillFavicons(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backfill-favicons", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "report what would change without writing anything")
	fs.BoolVar(dryRun, "n", false, "shorthand for --dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Like the other offline maintenance commands, this needs only the data dir
	// and DB; base-domain validation is irrelevant here.
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	scanned, updated, err := ingest.New(cfg, st).BackfillFavicons(ctx, *dryRun)
	if err != nil {
		return err
	}

	verb := "updated"
	if *dryRun {
		verb = "would update"
	}
	fmt.Printf("backfill-favicons: scanned %d sites, %s %d\n", scanned, verb, updated)
	return nil
}
