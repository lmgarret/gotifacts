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

// runMCP implements the headless MCP connection-management fallback (the portal
// offers the same under the Connections view).
func runMCP(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gotifacts mcp connections|revoke")
	}
	sub, rest := args[0], args[1:]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	switch sub {
	case "connections", "list":
		return mcpConnections(ctx, st)
	case "revoke":
		return mcpRevoke(ctx, st, rest)
	default:
		return fmt.Errorf("unknown mcp subcommand %q", sub)
	}
}

func mcpConnections(ctx context.Context, st *store.Store) error {
	conns, err := st.ListConnections(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tCLIENT\tUSER\tACCESS\tCREATED\tLAST USED\tEXPIRES")
	for _, c := range conns {
		client := c.ClientName
		if client == "" {
			client = c.ClientID
		}
		last := "never"
		if c.LastUsedAt != nil {
			last = c.LastUsedAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			c.ID, client, c.User, describeGrants(c.Grants),
			c.CreatedAt.Format(time.RFC3339), last, c.ExpiresAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

// describeGrants renders a connection's grants for the list view.
func describeGrants(grants []store.Grant) string {
	if len(grants) == 0 {
		return "-"
	}
	parts := make([]string, len(grants))
	for i, g := range grants {
		parts[i] = describeTarget(g) + ":" + keys.JoinCapabilities(g.Permissions)
	}
	return strings.Join(parts, " ")
}

func mcpRevoke(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("mcp revoke", flag.ContinueOnError)
	id := fs.String("id", "", "connection id to revoke (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	if err := st.DeleteConnection(ctx, *id); err != nil {
		return err
	}
	fmt.Printf("Revoked MCP connection %s\n", *id)
	return nil
}
