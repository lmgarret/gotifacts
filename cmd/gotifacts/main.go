// Command gotifacts is a self-hosted service that hosts static sites by
// host-based routing and serves a dynamic management portal.
//
// Usage:
//
//	gotifacts serve                                  run the HTTP server (default)
//	gotifacts keys create --name N [--admin | --grant G:CAPS | --grant-site S:CAPS]
//	gotifacts keys list
//	gotifacts keys revoke --id ID
package main

import (
	"context"
	"fmt"
	"os"
)

// Build-time version info, injected via -ldflags (see Dockerfile).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	ctx := context.Background()
	var err error
	switch cmd {
	case "serve":
		err = runServe(ctx, args)
	case "keys":
		err = runKeys(ctx, args)
	case "mcp":
		err = runMCP(ctx, args)
	case "migrate-layout":
		err = runMigrateLayout(ctx, args)
	case "version", "--version", "-version":
		fmt.Printf("gotifacts %s (commit %s, built %s)\n", version, commit, date)
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		usage(os.Stderr)
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gotifacts:", err)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `gotifacts — static site host + portal

Commands:
  serve                                   Run the HTTP server (default)
  keys create --name N [--admin] [--grant G:CAPS] [--grant-site S:CAPS]
                                          Create an API key (prints token once)
  keys list                               List API keys
  keys revoke --id ID                     Delete an API key
  mcp connections                         List active MCP connections
  mcp revoke --id ID                      Revoke an MCP connection
  migrate-layout [--dry-run]              Relocate site content into @site leaves

Configuration is via environment variables; see .env.example.
`)
}
