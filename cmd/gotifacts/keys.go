package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
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
	admin := fs.Bool("admin", false, "create a superuser key (full access; no grants)")
	var grantSpecs grantList
	fs.Var(&grantSpecs, "grant", "grant in the form 'group:cap1,cap2' (repeatable); caps: publish,unpublish,rollback,patch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	grants, err := grantSpecs.parse()
	if err != nil {
		return err
	}
	if err := validateKeyShape(*admin, grants); err != nil {
		return err
	}

	token, hash, err := keys.Generate()
	if err != nil {
		return err
	}
	rec, err := st.CreateKey(ctx, *name, *admin, grants, hash)
	if err != nil {
		return err
	}
	if rec.Admin {
		fmt.Printf("Created admin API key #%d (%s)", rec.ID, rec.Name)
	} else {
		fmt.Printf("Created API key #%d (%s)", rec.ID, rec.Name)
		for _, g := range rec.Grants {
			grp := g.Group
			if grp == "" {
				grp = "*"
			}
			fmt.Printf("\n  grant: %s -> %s", grp, keys.JoinCapabilities(g.Permissions))
		}
	}
	fmt.Printf("\n\n  %s\n\nThis token is shown only once. Store it securely.\n", token)
	return nil
}

// grantList collects repeated --grant flags ("group:cap1,cap2").
type grantList []string

func (g *grantList) String() string { return strings.Join(*g, " ") }
func (g *grantList) Set(v string) error {
	*g = append(*g, v)
	return nil
}

func (g grantList) parse() ([]store.Grant, error) {
	var out []store.Grant
	for _, spec := range g {
		group, capsCSV, found := strings.Cut(spec, ":")
		if !found {
			return nil, fmt.Errorf("invalid --grant %q (expected 'group:cap1,cap2')", spec)
		}
		caps, err := keys.ParseCapabilities(capsCSV)
		if err != nil {
			return nil, fmt.Errorf("invalid capabilities in --grant %q: %w", spec, err)
		}
		out = append(out, store.Grant{Group: strings.Trim(strings.TrimSpace(group), "/"), Permissions: caps})
	}
	return out, nil
}

// validateKeyShape mirrors the server-side invariants for headless key creation.
func validateKeyShape(admin bool, grants []store.Grant) error {
	if admin {
		if len(grants) > 0 {
			return fmt.Errorf("--admin keys must not specify --grant")
		}
		return nil
	}
	if len(grants) == 0 {
		return fmt.Errorf("specify --admin or at least one --grant")
	}
	for _, gr := range grants {
		if gr.Group == "" && !keys.OnlyPublish(gr.Permissions) {
			return fmt.Errorf("a grant with unpublish/rollback/patch must specify a group")
		}
	}
	return nil
}

func keysList(ctx context.Context, st *store.Store) error {
	ks, err := st.ListKeys(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tACCESS\tCREATED\tLAST USED")
	for _, k := range ks {
		last := "never"
		if k.LastUsedAt != nil {
			last = k.LastUsedAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", k.ID, k.Name, describeAccess(k), k.CreatedAt.Format(time.RFC3339), last)
	}
	return tw.Flush()
}

// describeAccess renders a key's privilege for the list view.
func describeAccess(k store.APIKey) string {
	if k.Admin {
		return "admin"
	}
	if len(k.Grants) == 0 {
		return "-"
	}
	parts := make([]string, len(k.Grants))
	for i, g := range k.Grants {
		grp := g.Group
		if grp == "" {
			grp = "*"
		}
		parts[i] = grp + ":" + keys.JoinCapabilities(g.Permissions)
	}
	return strings.Join(parts, " ")
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
