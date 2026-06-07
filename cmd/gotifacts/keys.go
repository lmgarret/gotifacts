package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

// runKeys implements the headless key-management fallback.
func runKeys(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gotifacts keys create|list|revoke")
	}
	sub, rest := args[0], args[1:]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Key management needs only the DB path; base-domain validation is skipped.
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	switch sub {
	case "create":
		return keysCreate(ctx, st, rest)
	case "list":
		return keysList(ctx, st)
	case "revoke":
		return keysRevoke(ctx, st, rest)
	default:
		return fmt.Errorf("unknown keys subcommand %q", sub)
	}
}

func keysCreate(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("keys create", flag.ContinueOnError)
	name := fs.String("name", "", "human-readable key name (required)")
	scopeStr := fs.String("scope", "", "key scope: admin | publish (required)")
	group := fs.String("group", "", "group restriction for publish-scoped keys (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	scope, err := keys.ParseScope(*scopeStr)
	if err != nil {
		return fmt.Errorf("--scope must be 'admin' or 'publish'")
	}
	if scope == keys.ScopeAdmin && *group != "" {
		return fmt.Errorf("--group is only valid for publish scope")
	}
	token, hash, err := keys.Generate()
	if err != nil {
		return err
	}
	rec, err := st.CreateKey(ctx, *name, scope, *group, hash)
	if err != nil {
		return err
	}
	fmt.Printf("Created API key #%d (%s, scope=%s", rec.ID, rec.Name, rec.Scope)
	if rec.GroupRestriction != "" {
		fmt.Printf(", group=%s", rec.GroupRestriction)
	}
	fmt.Printf(")\n\n  %s\n\nThis token is shown only once. Store it securely.\n", token)
	return nil
}

func keysList(ctx context.Context, st *store.Store) error {
	ks, err := st.ListKeys(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tSCOPE\tGROUP\tCREATED\tLAST USED")
	for _, k := range ks {
		last := "never"
		if k.LastUsedAt != nil {
			last = k.LastUsedAt.Format(time.RFC3339)
		}
		grp := k.GroupRestriction
		if grp == "" {
			grp = "-"
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", k.ID, k.Name, k.Scope, grp, k.CreatedAt.Format(time.RFC3339), last)
	}
	return tw.Flush()
}

func keysRevoke(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("keys revoke", flag.ContinueOnError)
	id := fs.Int64("id", 0, "key id to revoke (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return fmt.Errorf("--id is required")
	}
	if err := st.DeleteKey(ctx, *id); err != nil {
		return err
	}
	fmt.Printf("Revoked API key #%d\n", *id)
	return nil
}
