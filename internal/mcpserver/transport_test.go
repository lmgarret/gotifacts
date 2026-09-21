package mcpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/config"
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
		Hash: keys.Hash(token), ConnID: "conn-smoke", Kind: "access", ClientID: "c", User: "tester",
		Grants:    []store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}},
		ExpiresAt: time.Now().Add(time.Hour),
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
	if _, err := os.Stat(filepath.Join(cfg.SitesDir(), "claude", "xport", "@site", "index.html")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
}

// TestPublishLargeBodyOverMCPTransport guards the Streamable HTTP request body
// limit. go-sdk v1.7.0 started bounding it, defaulting to 4 MiB; the handler
// raises that to GOTIFACTS_MAX_UPLOAD_BYTES so a multi-file site does not get a
// 413 before reaching the ingest pipeline. The payload here sits above the SDK
// default and well below the configured limit.
func TestPublishLargeBodyOverMCPTransport(t *testing.T) {
	s, cfg := newTestService(t)
	ctx := context.Background()

	const token = "large-access-token"
	if err := s.store.CreateToken(ctx, store.Token{
		Hash: keys.Hash(token), ConnID: "conn-large", Kind: "access", ClientID: "c", User: "tester",
		Grants:    []store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}},
		ExpiresAt: time.Now().Add(time.Hour),
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

	// ~5 MiB of HTML: over mcpsdk.DefaultMaxRequestBodyBytes, under MaxUploadBytes.
	body := strings.Repeat("x", 5<<20)
	if int64(len(body)) <= mcpsdk.DefaultMaxRequestBodyBytes || int64(len(body)) >= cfg.MaxUploadBytes {
		t.Fatalf("payload of %d bytes does not straddle the two limits", len(body))
	}
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "publish_site",
		Arguments: map[string]any{"slug": "big", "html": "<!doctype html><p>" + body + "</p>"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	if _, err := os.Stat(filepath.Join(cfg.SitesDir(), "claude", "big", "@site", "index.html")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
}

// elicitingClient dials the test server with an elicitation handler, which
// makes the SDK advertise the elicitation capability the purge confirmation
// gate keys off. answer is returned for every elicitation; calls records the
// messages the server asked about.
func elicitingClient(t *testing.T, ctx context.Context, url, token string, answer string, calls *[]string) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "eliciting-client", Version: "1.2.3"}, &mcpsdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			*calls = append(*calls, req.Params.Message)
			return &mcpsdk.ElicitResult{Action: answer}, nil
		},
	})
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   url + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// newPurgeFixture stands up a service with a token granting publish, unpublish
// and purge, publishes a site, and unpublishes it so it sits in quarantine
// ready to be purged.
func newPurgeFixture(t *testing.T) (*Service, *config.Config, string, string) {
	t.Helper()
	s, cfg := newTestService(t)
	ctx := context.Background()

	token := "purge-access-token"
	if err := s.store.CreateToken(ctx, store.Token{
		Hash: keys.Hash(token), ConnID: "conn-purge", Kind: "access", ClientID: "c", User: "tester",
		Grants: []store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{
			keys.CapPublish, keys.CapUnpublish, keys.CapPurge,
		}}},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	t.Cleanup(srv.Close)

	req := mcpReq(&auth.Principal{User: "tester", Source: auth.SourceAPIKey, Grants: []store.Grant{{
		Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish},
	}}})
	if res, _, err := s.publishSite(ctx, req, publishInput{Slug: "doomed", HTML: "<!doctype html><h1>bye</h1>"}); err != nil || res.IsError {
		t.Fatalf("publish setup: %v / %+v", err, res)
	}
	if res, _, err := s.unpublishSite(ctx, req, unpublishInput{Slug: "doomed"}); err != nil || res.IsError {
		t.Fatalf("unpublish setup: %v / %+v", err, res)
	}
	return s, cfg, srv.URL, token
}

// TestPurgeSiteConfirmAccept drives the SEP-2322 round trip end to end: the
// server answers the first purge_site call with an input request, the SDK
// client fulfills it through its elicitation handler and retries transparently,
// and only then are the quarantined files destroyed.
func TestPurgeSiteConfirmAccept(t *testing.T) {
	_, cfg, url, token := newPurgeFixture(t)
	ctx := context.Background()

	quarantine := filepath.Join(cfg.DeletedDir(), "claude", "doomed")
	if _, err := os.Stat(quarantine); err != nil {
		t.Fatalf("quarantine dir missing before purge: %v", err)
	}

	var asked []string
	session := elicitingClient(t, ctx, url, token, "accept", &asked)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "purge_site",
		Arguments: map[string]any{"slug": "doomed"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	if len(asked) != 1 {
		t.Fatalf("expected exactly one confirmation prompt, got %d: %v", len(asked), asked)
	}
	if !strings.Contains(asked[0], `"doomed"`) || !strings.Contains(asked[0], "cannot be recovered") {
		t.Errorf("confirmation prompt does not name the target and its finality: %q", asked[0])
	}
	if _, err := os.Stat(quarantine); !os.IsNotExist(err) {
		t.Fatalf("quarantine dir should be gone after a confirmed purge: %v", err)
	}
}

// TestPurgeSiteConfirmDecline checks that declining leaves the site intact and
// reports that plainly rather than as a tool error.
func TestPurgeSiteConfirmDecline(t *testing.T) {
	_, cfg, url, token := newPurgeFixture(t)
	ctx := context.Background()

	var asked []string
	session := elicitingClient(t, ctx, url, token, "decline", &asked)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "purge_site",
		Arguments: map[string]any{"slug": "doomed"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("a declined purge is not a tool error: %+v", res.Content)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "cancelled") {
		t.Errorf("expected the result to say the purge was cancelled, got %q", text)
	}
	if _, err := os.Stat(filepath.Join(cfg.DeletedDir(), "claude", "doomed")); err != nil {
		t.Fatalf("quarantine dir should survive a declined purge: %v", err)
	}
}

// TestToolListCacheHint checks the SEP-2549 hint the server stamps on
// tools/list, so clients stop re-listing a tool set that never changes.
func TestToolListCacheHint(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	const token = "list-access-token"
	if err := s.store.CreateToken(ctx, store.Token{
		Hash: keys.Hash(token), ConnID: "conn-list", Kind: "access", ClientID: "c", User: "tester",
		Grants:    []store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("expected the server to advertise tools")
	}
	if got, want := res.TTLMs, int(toolListTTL.Milliseconds()); got != want {
		t.Errorf("TTLMs = %d, want %d", got, want)
	}
	if res.CacheScope != "public" {
		t.Errorf("CacheScope = %q, want %q", res.CacheScope, "public")
	}
}

// TestAuditLogRecordsClient checks that a tool call's audit line names the MCP
// client behind it, not just the authenticated user.
func TestAuditLogRecordsClient(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))

	const token = "audit-access-token"
	if err := s.store.CreateToken(ctx, store.Token{
		Hash: keys.Hash(token), ConnID: "conn-audit", Kind: "access", ClientID: "c", User: "tester",
		Grants:    []store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "audited-client", Version: "4.5.6"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	if _, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "publish_site",
		Arguments: map[string]any{"slug": "audited", "html": "<!doctype html><h1>x</h1>"},
	}); err != nil {
		t.Fatalf("call tool: %v", err)
	}

	got := logs.String()
	for _, want := range []string{"site published", "client=audited-client", "client_version=4.5.6", "mcp_protocol="} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log missing %q; got:\n%s", want, got)
		}
	}
}
