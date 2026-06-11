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
	"github.com/lmgarret/gotifacts/internal/router"
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
	var groupSpecs, siteSpecs specList
	fs.Var(&groupSpecs, "grant", "group grant 'group:cap1,cap2' (repeatable); empty group = all sites; caps: publish,unpublish,rollback,patch")
	fs.Var(&siteSpecs, "grant-site", "site grant 'group/slug:cap1,cap2' (repeatable); confined to that one site")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	grants, err := parseGrantSpecs(groupSpecs, store.GrantGroup)
	if err != nil {
		return err
	}
	siteGrants, err := parseGrantSpecs(siteSpecs, store.GrantSite)
	if err != nil {
		return err
	}
	grants = append(grants, siteGrants...)
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
			fmt.Printf("\n  grant: %s -> %s", describeTarget(g), keys.JoinCapabilities(g.Permissions))
		}
	}
	fmt.Printf("\n\n  %s\n\nThis token is shown only once. Store it securely.\n", token)
	return nil
}

// specList collects repeated grant flags of a single kind.
type specList []string

func (s *specList) String() string { return strings.Join(*s, " ") }
func (s *specList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// parseGrantSpecs turns "target:cap1,cap2" specs into grants of the given kind,
// normalizing and validating each target.
func parseGrantSpecs(specs specList, kind store.GrantKind) ([]store.Grant, error) {
	var out []store.Grant
	for _, spec := range specs {
		target, capsCSV, found := strings.Cut(spec, ":")
		if !found {
			return nil, fmt.Errorf("invalid grant %q (expected 'target:cap1,cap2')", spec)
		}
		caps, err := keys.ParseCapabilities(capsCSV)
		if err != nil {
			return nil, fmt.Errorf("invalid capabilities in grant %q: %w", spec, err)
		}
		g, err := buildGrant(kind, target, caps)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// buildGrant normalizes and validates a grant target for its kind. A site grant
// requires a valid site path; a group grant accepts a valid group path or an
// empty target (meaning "all sites").
func buildGrant(kind store.GrantKind, target string, caps []keys.Capability) (store.Grant, error) {
	t := strings.Trim(strings.ToLower(strings.TrimSpace(target)), "/")
	if kind == store.GrantSite {
		if t == "" {
			return store.Grant{}, fmt.Errorf("a site grant requires a target")
		}
	}
	if t != "" {
		sp, err := normalizeSitePath(t)
		if err != nil {
			return store.Grant{}, fmt.Errorf("invalid %s target %q: %w", kind, target, err)
		}
		t = sp
	}
	return store.Grant{Kind: kind, Target: t, Permissions: caps}, nil
}

// normalizeSitePath validates a slash path and returns its canonical form,
// reusing the router's site-path rules (labels, depth).
func normalizeSitePath(path string) (string, error) {
	segs := strings.Split(path, "/")
	slug := segs[len(segs)-1]
	group := strings.Join(segs[:len(segs)-1], "/")
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		return "", err
	}
	return sp.Dir(), nil
}

// validateKeyShape mirrors the server-side key-level invariants.
func validateKeyShape(admin bool, grants []store.Grant) error {
	if admin {
		if len(grants) > 0 {
			return fmt.Errorf("--admin keys must not specify grants")
		}
		return nil
	}
	if len(grants) == 0 {
		return fmt.Errorf("specify --admin or at least one --grant/--grant-site")
	}
	return nil
}

// describeTarget renders a grant's target for display.
func describeTarget(g store.Grant) string {
	if g.Kind == store.GrantSite {
		return "site " + g.Target
	}
	if g.Target == "" {
		return "all sites"
	}
	return "group " + g.Target
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
		parts[i] = describeTarget(g) + ":" + keys.JoinCapabilities(g.Permissions)
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
