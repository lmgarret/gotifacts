package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearerRoundTripper injects a static bearer token, standing in for the access
// token an MCP client would carry after the OAuth flow.
type bearerRoundTripper struct{ token string }

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestPublishOverMCPTransport exercises the real Streamable HTTP transport: an
// SDK MCP client connects with a bearer access token and calls publish_site,
// validating the bearer middleware, JSON-RPC handshake, and tool dispatch
// end-to-end.
func TestPublishOverMCPTransport(t *testing.T) {
	s, cfg := newTestService(t)
	ctx := context.Background()

	const token = "smoke-access-token"
	if err := s.store.CreateToken(ctx, store.Token{
		Hash: keys.Hash(token), Kind: "access", ClientID: "c", User: "tester",
		Scope: scopePublish, Group: "claude", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "publish_site",
		Arguments: map[string]any{"slug": "xport", "html": "<!doctype html><h1>x</h1>"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "https://xport.claude.example.com") {
		t.Fatalf("unexpected tool output: %q", text)
	}
	if _, err := os.Stat(filepath.Join(cfg.SitesDir(), "claude", "xport", "index.html")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
}
