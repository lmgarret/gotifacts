package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/store"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestService(t *testing.T) (*Service, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		BaseDomain:         "example.com",
		DataDir:            t.TempDir(),
		MaxUploadBytes:     config.DefaultMaxUploadBytes,
		MaxExtractBytes:    config.DefaultMaxExtractBytes,
		MaxExtractEntries:  config.DefaultMaxExtractEntries,
		MCPEnabled:         true,
		MCPGroup:           "claude",
		MCPAccessTokenTTL:  time.Hour,
		MCPRefreshTokenTTL: 24 * time.Hour,
		AdminUsers:         []string{"tester"},
	}
	st, err := store.Open(context.Background(), filepath.Join(cfg.DataDir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc, err := New(cfg, st, ingest.New(cfg, st), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, cfg
}

// testHandler registers the public routes plus a stub forward-auth wrapper for
// the consent endpoint that injects the given principal.
func (s *Service) testHandler(p *auth.Principal) http.Handler {
	mux := http.NewServeMux()
	s.RegisterPublic(mux)
	mux.HandleFunc("GET /mcp/oauth/authorize", func(w http.ResponseWriter, r *http.Request) { s.HandleAuthorize(w, r, p) })
	mux.HandleFunc("POST /mcp/oauth/authorize", func(w http.ResponseWriter, r *http.Request) { s.HandleAuthorize(w, r, p) })
	return mux
}

func TestHelpers(t *testing.T) {
	verifier := "this-is-a-sufficiently-long-pkce-code-verifier-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !pkceVerify(verifier, challenge) {
		t.Fatal("pkceVerify rejected a valid verifier")
	}
	if pkceVerify("wrong", challenge) {
		t.Fatal("pkceVerify accepted a wrong verifier")
	}

	for _, tc := range []struct {
		uri  string
		want bool
	}{
		{"https://claude.ai/cb", true},
		{"http://localhost:8080/cb", true},
		{"http://127.0.0.1/cb", true},
		{"http://evil.example/cb", false},
		{"https://claude.ai/cb#frag", false},
		{"ftp://x/cb", false},
	} {
		if got := validRedirectURI(tc.uri); got != tc.want {
			t.Errorf("validRedirectURI(%q)=%v want %v", tc.uri, got, tc.want)
		}
	}

	s, _ := newTestService(t)
	tok := s.csrfToken("alice")
	if !s.csrfValid("alice", tok) {
		t.Fatal("csrf token did not validate")
	}
	if s.csrfValid("bob", tok) {
		t.Fatal("csrf token validated for a different user")
	}

	rl := newRateLimiter(2, time.Minute)
	first, second, third := rl.allow(), rl.allow(), rl.allow()
	if !first || !second || third {
		t.Fatalf("rate limiter did not cap at 2: %v %v %v", first, second, third)
	}
}

func TestMetadataEndpoints(t *testing.T) {
	s, _ := newTestService(t)
	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()

	var as map[string]any
	getJSON(t, srv.URL+"/.well-known/oauth-authorization-server", &as)
	if as["issuer"] != "https://example.com" {
		t.Fatalf("issuer = %v", as["issuer"])
	}
	if as["authorization_endpoint"] != "https://example.com/mcp/oauth/authorize" {
		t.Fatalf("authorization_endpoint = %v", as["authorization_endpoint"])
	}
	if as["token_endpoint"] != "https://example.com/mcp/oauth/token" {
		t.Fatalf("token_endpoint = %v", as["token_endpoint"])
	}

	var prm map[string]any
	getJSON(t, srv.URL+"/.well-known/oauth-protected-resource", &prm)
	if prm["resource"] != "https://example.com/mcp" {
		t.Fatalf("resource = %v", prm["resource"])
	}
}

func TestMCPRequiresBearer(t *testing.T) {
	s, _ := newTestService(t)
	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()

	resp, err := http.DefaultClient.Do(ctxReq(t, http.MethodGet, srv.URL+"/mcp", nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, "resource_metadata=") {
		t.Fatalf("WWW-Authenticate missing resource_metadata: %q", wa)
	}
}

func TestOAuthFlow(t *testing.T) {
	s, _ := newTestService(t)
	srv := httptest.NewServer(s.testHandler(&auth.Principal{User: "tester"}))
	defer srv.Close()

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	const redirectURI = "https://claude.ai/cb"

	// 1. Dynamic client registration.
	var reg registerResponse
	postJSON(t, srv.URL+"/mcp/oauth/register", registerRequest{RedirectURIs: []string{redirectURI}, ClientName: "Claude"}, &reg)
	if reg.ClientID == "" {
		t.Fatal("no client_id issued")
	}

	// 2. PKCE pair.
	verifier := "this-is-a-sufficiently-long-pkce-code-verifier-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// 3. Consent (GET renders, POST approves).
	authURL := srv.URL + "/mcp/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {reg.ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
	}.Encode()
	if resp, err := http.DefaultClient.Do(ctxReq(t, http.MethodGet, authURL, nil, "")); err != nil {
		t.Fatal(err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("consent GET status = %d", resp.StatusCode)
		}
	}

	form := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
		"csrf":                  {s.csrfToken("tester")},
		"action":                {"approve"},
	}
	resp, err := noRedirect.Do(ctxReq(t, http.MethodPost, srv.URL+"/mcp/oauth/authorize",
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("approve status = %d, want 302", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "xyz" {
		t.Fatalf("state not echoed: %q", loc.Query().Get("state"))
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}

	// 4. Token exchange.
	var tok tokenResponse
	postForm(t, srv.URL+"/mcp/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {reg.ClientID},
		"code_verifier": {verifier},
	}, &tok)
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.TokenType != "Bearer" {
		t.Fatalf("bad token response: %+v", tok)
	}

	// The access token must verify and carry the configured group.
	ti, err := s.verifyToken(context.Background(), tok.AccessToken, nil)
	if err != nil {
		t.Fatalf("verifyToken: %v", err)
	}
	if ti.Extra["group"] != "claude" {
		t.Fatalf("token group = %v", ti.Extra["group"])
	}

	// Wrong PKCE verifier must be rejected on a fresh code.
	if codeReuse := tryReuse(t, srv.URL, reg.ClientID, redirectURI, code); codeReuse {
		t.Fatal("authorization code was reusable")
	}

	// 5. Refresh grant rotates and returns a new access token.
	var refreshed tokenResponse
	postForm(t, srv.URL+"/mcp/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {reg.ClientID},
	}, &refreshed)
	if refreshed.AccessToken == "" || refreshed.AccessToken == tok.AccessToken {
		t.Fatalf("refresh did not issue a new access token: %+v", refreshed)
	}
}

func TestPublishSiteTool(t *testing.T) {
	s, cfg := newTestService(t)
	ctx := context.Background()
	req := &mcpsdk.CallToolRequest{Extra: &mcpsdk.RequestExtra{
		TokenInfo: &mcpauth.TokenInfo{UserID: "tester", Extra: map[string]any{"group": "claude"}},
	}}

	res, out, err := s.publishSite(ctx, req, publishInput{Slug: "report", HTML: "<!doctype html><h1>hi</h1>"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if out.URL != "https://report.claude.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
	if _, err := os.Stat(filepath.Join(cfg.SitesDir(), "claude", "report", "index.html")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}

	// Publishing outside the token's group restriction is denied.
	res, _, err = s.publishSite(ctx, req, publishInput{Slug: "x", Group: "other", HTML: "<h1>no</h1>"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error publishing outside group restriction")
	}

	// Empty HTML is rejected.
	res, _, _ = s.publishSite(ctx, req, publishInput{Slug: "y", HTML: "  "})
	if !res.IsError {
		t.Fatal("expected error for empty html")
	}
}

// ctxReq builds a request carrying a context (satisfies the noctx linter, which
// forbids the context-less http.Get/Post/PostForm helpers).
func ctxReq(t *testing.T, method, url string, body io.Reader, contentType string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

// tryReuse attempts to exchange an already-consumed code; returns true if it
// unexpectedly succeeds.
func tryReuse(t *testing.T, base, clientID, redirectURI, code string) bool {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {"this-is-a-sufficiently-long-pkce-code-verifier-1234567890"},
	}
	resp, err := http.DefaultClient.Do(ctxReq(t, http.MethodPost, base+"/mcp/oauth/token",
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(ctxReq(t, http.MethodGet, url, nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func postJSON(t *testing.T, url string, body, dst any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.DefaultClient.Do(ctxReq(t, http.MethodPost, url,
		strings.NewReader(string(b)), "application/json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s status %d: %s", url, resp.StatusCode, msg)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func postForm(t *testing.T, url string, form url.Values, dst any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(ctxReq(t, http.MethodPost, url,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s status %d: %s", url, resp.StatusCode, msg)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
