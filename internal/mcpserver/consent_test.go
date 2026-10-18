package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

const consentRedirectURI = "https://claude.ai/cb"

// consentEnv is a running test server with one registered OAuth client and a
// PKCE pair, for driving the consent endpoints.
type consentEnv struct {
	t        *testing.T
	s        *Service
	srv      *httptest.Server
	user     string
	reg      registerResponse
	verifier string
}

// newConsentEnv serves s with principal p injected and registers a client.
func newConsentEnv(t *testing.T, s *Service, p *auth.Principal) *consentEnv {
	t.Helper()
	srv := httptest.NewServer(s.testHandler(p))
	t.Cleanup(srv.Close)
	e := &consentEnv{
		t: t, s: s, srv: srv, user: p.User,
		verifier: "this-is-a-sufficiently-long-pkce-code-verifier-1234567890",
	}
	postJSON(t, srv.URL+"/mcp/oauth/register", registerRequest{RedirectURIs: []string{consentRedirectURI}, ClientName: "Claude"}, &e.reg)
	return e
}

func (e *consentEnv) challenge() string {
	sum := sha256.Sum256([]byte(e.verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// query is the standard, valid authorization request query.
func (e *consentEnv) query() url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {e.reg.ClientID},
		"redirect_uri":          {consentRedirectURI},
		"code_challenge":        {e.challenge()},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
	}
}

// body is a valid consent submission for the given action and grants.
func (e *consentEnv) body(action string, grants []consentGrant, ackGlobal bool) consentRequest {
	return consentRequest{
		ClientID:            e.reg.ClientID,
		RedirectURI:         consentRedirectURI,
		CodeChallenge:       e.challenge(),
		CodeChallengeMethod: "S256",
		State:               "xyz",
		CSRF:                e.s.csrfToken(e.user),
		Action:              action,
		Grants:              grants,
		AcknowledgeGlobal:   ackGlobal,
	}
}

// noRedirect is a client that surfaces redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// get fetches path without following redirects.
func (e *consentEnv) get(path string) *http.Response {
	e.t.Helper()
	resp, err := noRedirect.Do(ctxReq(e.t, http.MethodGet, e.srv.URL+path, nil, ""))
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

// getBody fetches path and returns the status and body.
func (e *consentEnv) getBody(path string) (int, string) {
	e.t.Helper()
	resp := e.get(path)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// post sends a consent submission (any JSON-encodable body).
func (e *consentEnv) post(body any) (int, map[string]string) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := noRedirect.Do(ctxReq(e.t, http.MethodPost, e.srv.URL+"/api/mcp/consent", bytes.NewReader(b), "application/json"))
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]string{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// redirect submits req, expects 200 {redirect_url} and returns the query of that
// URL (after checking it points at the client's redirect URI).
func (e *consentEnv) redirect(req consentRequest) url.Values {
	e.t.Helper()
	status, out := e.post(req)
	if status != http.StatusOK {
		e.t.Fatalf("POST /api/mcp/consent = %d %v, want 200", status, out)
	}
	u, err := url.Parse(out["redirect_url"])
	if err != nil || !strings.HasPrefix(out["redirect_url"], consentRedirectURI+"?") {
		e.t.Fatalf("redirect_url = %q (%v), want %s?...", out["redirect_url"], err, consentRedirectURI)
	}
	return u.Query()
}

// principalFor exchanges code for a token and returns the token's principal.
func (e *consentEnv) principalFor(code string) *auth.Principal {
	e.t.Helper()
	var tok tokenResponse
	postForm(e.t, e.srv.URL+"/mcp/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {consentRedirectURI},
		"client_id":     {e.reg.ClientID},
		"code_verifier": {e.verifier},
	}, &tok)
	ti, err := e.s.verifyToken(context.Background(), tok.AccessToken, nil)
	if err != nil {
		e.t.Fatalf("verifyToken: %v", err)
	}
	p, _ := ti.Extra["principal"].(*auth.Principal)
	if p == nil {
		e.t.Fatal("token has no principal")
	}
	return p
}

// consentResult is the outcome of one approval.
type consentResult struct {
	errCode, errDesc string
	principal        *auth.Principal
}

// approveConsent approves with the given grants and, when a code was issued,
// returns the resulting token's principal.
func approveConsent(t *testing.T, s *Service, grants []consentGrant, ackGlobal bool) consentResult {
	t.Helper()
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	q := e.redirect(e.body("approve", grants, ackGlobal))
	if code := q.Get("error"); code != "" {
		return consentResult{errCode: code, errDesc: q.Get("error_description")}
	}
	return consentResult{principal: e.principalFor(q.Get("code"))}
}

func grant(kind, target string, perms ...string) consentGrant {
	return consentGrant{Kind: kind, Target: target, Permissions: perms}
}

func TestConsentAuthorizePage(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})

	if status, body := e.getBody("/mcp/oauth/authorize?" + e.query().Encode()); status != http.StatusOK || body != testSPAMarker {
		t.Fatalf("valid request = %d %q, want 200 with the SPA", status, body)
	}

	// Before the redirect URI is trusted: an error response, never a redirect.
	for name, mod := range map[string]func(url.Values){
		"missing client":  func(q url.Values) { q.Del("client_id") },
		"unknown client":  func(q url.Values) { q.Set("client_id", "nope") },
		"foreign uri":     func(q url.Values) { q.Set("redirect_uri", "https://evil.example/cb") },
		"missing the uri": func(q url.Values) { q.Del("redirect_uri") },
	} {
		q := e.query()
		mod(q)
		resp := e.get("/mcp/oauth/authorize?" + q.Encode())
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
			t.Errorf("%s: status %d, Location %q; want 400 without redirect", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

func TestConsentAuthorizeRedirectsTrustedErrors(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	for name, tc := range map[string]struct {
		mod  func(url.Values)
		code string
	}{
		"response type": {func(q url.Values) { q.Set("response_type", "token") }, "unsupported_response_type"},
		"no challenge":  {func(q url.Values) { q.Del("code_challenge") }, "invalid_request"},
		"plain method":  {func(q url.Values) { q.Set("code_challenge_method", "plain") }, "invalid_request"},
	} {
		q := e.query()
		tc.mod(q)
		resp := e.get("/mcp/oauth/authorize?" + q.Encode())
		_ = resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusFound || loc == nil || loc.Query().Get("error") != tc.code || loc.Query().Get("state") != "xyz" {
			t.Errorf("%s: status %d, Location %q; want 302 with error=%s", name, resp.StatusCode, resp.Header.Get("Location"), tc.code)
		}
	}
}

func TestConsentEndpointsForbiddenForOtherUsers(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "stranger"})

	if status, _ := e.getBody("/mcp/oauth/authorize?" + e.query().Encode()); status != http.StatusForbidden {
		t.Errorf("authorize page = %d, want 403", status)
	}
	if status, _ := e.getBody("/api/mcp/consent?" + e.query().Encode()); status != http.StatusForbidden {
		t.Errorf("consent info = %d, want 403", status)
	}
	if status, _ := e.post(e.body("approve", []consentGrant{grant("group", "claude", "publish")}, false)); status != http.StatusForbidden {
		t.Errorf("consent submit = %d, want 403", status)
	}
}

func TestConsentInfo(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})

	var info consentInfo
	getJSON(t, e.srv.URL+"/api/mcp/consent?"+e.query().Encode(), &info)
	if info.ClientName != "Claude" || info.User != "tester" || info.BaseDomain != "example.com" || info.DefaultTarget != "claude" {
		t.Fatalf("info = %+v", info)
	}
	if info.CSRF != s.csrfToken("tester") {
		t.Fatal("csrf token does not match the user's token")
	}
	for _, want := range []keys.Capability{keys.CapPublish, keys.CapRead} {
		if !keys.HasCapability(info.Capabilities, want) {
			t.Errorf("capabilities %v missing %q", info.Capabilities, want)
		}
	}
	if info.Capabilities[0] != keys.CapPublish {
		t.Errorf("first capability = %q, want publish", info.Capabilities[0])
	}
}

func TestConsentInfoInvalidParams(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	for name, mod := range map[string]func(url.Values){
		"unknown client": func(q url.Values) { q.Set("client_id", "nope") },
		"foreign uri":    func(q url.Values) { q.Set("redirect_uri", "https://evil.example/cb") },
		"no challenge":   func(q url.Values) { q.Del("code_challenge") },
		"bad method":     func(q url.Values) { q.Set("code_challenge_method", "plain") },
		"response type":  func(q url.Values) { q.Set("response_type", "token") },
	} {
		q := e.query()
		mod(q)
		resp := e.get("/api/mcp/consent?" + q.Encode())
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || out["error"] == "" || resp.Header.Get("Location") != "" {
			t.Errorf("%s: status %d, body %v; want 400 JSON error without redirect", name, resp.StatusCode, out)
		}
	}
}

func TestConsentSubmitRejectsBadRequests(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	ok := []consentGrant{grant("group", "claude", "publish")}

	badCSRF := e.body("approve", ok, false)
	badCSRF.CSRF = "forged"
	badAction := e.body("maybe", ok, false)
	badClient := e.body("approve", ok, false)
	badClient.ClientID = "nope"
	badURI := e.body("approve", ok, false)
	badURI.RedirectURI = "https://evil.example/cb"
	for name, req := range map[string]consentRequest{
		"bad csrf": badCSRF, "bad action": badAction, "unknown client": badClient, "foreign redirect uri": badURI,
	} {
		if status, out := e.post(req); status != http.StatusBadRequest || out["error"] == "" {
			t.Errorf("%s: status %d, body %v; want 400 JSON error", name, status, out)
		}
	}

	resp, err := noRedirect.Do(ctxReq(t, http.MethodPost, e.srv.URL+"/api/mcp/consent", strings.NewReader("{nope"), "application/json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed JSON: status %d, want 400", resp.StatusCode)
	}
}

func TestConsentSubmitPKCEErrorRedirects(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	req := e.body("approve", []consentGrant{grant("group", "claude", "publish")}, false)
	req.CodeChallengeMethod = "plain"
	q := e.redirect(req)
	if q.Get("error") != "invalid_request" || q.Get("state") != "xyz" || q.Get("code") != "" {
		t.Fatalf("redirect query = %v, want invalid_request with state and no code", q)
	}
}

func TestConsentDeny(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	q := e.redirect(e.body("deny", nil, false))
	if q.Get("error") != "access_denied" || q.Get("state") != "xyz" || q.Get("code") != "" {
		t.Fatalf("redirect query = %v, want access_denied with state and no code", q)
	}
}

func TestConsentSeveralGrants(t *testing.T) {
	s, _ := newTestService(t)
	res := approveConsent(t, s, []consentGrant{
		grant("group", "claude", "publish"),
		grant("group", "docs", "read"),
	}, false)
	if res.errCode != "" {
		t.Fatalf("unexpected error: %s: %s", res.errCode, res.errDesc)
	}
	p := res.principal
	if len(p.Grants) != 2 {
		t.Fatalf("grants = %+v, want 2", p.Grants)
	}
	if !p.CanPublishTo("claude", "report") || p.CanPublishTo("docs", "app") {
		t.Fatal("token should publish to claude only")
	}
	if !p.Can(keys.CapRead, "docs", "app") {
		t.Fatal("token should read docs")
	}
	if p.Can(keys.CapRead, "claude", "report") {
		t.Fatal("publish on claude must not imply read")
	}
}

func TestConsentReadIsRegularPermission(t *testing.T) {
	s, _ := newTestService(t)
	res := approveConsent(t, s, []consentGrant{grant("site", " Docs/App/ ", "read", "publish", "read")}, false)
	if res.errCode != "" {
		t.Fatalf("unexpected error: %s: %s", res.errCode, res.errDesc)
	}
	g := res.principal.Grants
	if len(g) != 1 || g[0].Kind != store.GrantSite || g[0].Target != "docs/app" || len(g[0].Permissions) != 2 {
		t.Fatalf("grants = %+v, want one normalised site grant with read+publish", g)
	}
	if !res.principal.Can(keys.CapRead, "docs", "app") || res.principal.Can(keys.CapRead, "docs", "other") {
		t.Fatal("read must be confined to the site")
	}
}

func TestConsentGlobalGrantNeedsAcknowledgement(t *testing.T) {
	s, _ := newTestService(t)
	global := []consentGrant{grant("group", "", "read")}

	res := approveConsent(t, s, global, false)
	if res.errCode != "invalid_scope" || !strings.Contains(res.errDesc, "global-scope risk") {
		t.Fatalf("without acknowledgement: got %q / %q, want invalid_scope about the global-scope risk", res.errCode, res.errDesc)
	}
	res = approveConsent(t, s, global, true)
	if res.errCode != "" {
		t.Fatalf("with acknowledgement: unexpected error %s: %s", res.errCode, res.errDesc)
	}
	if !res.principal.Can(keys.CapRead, "any", "site") || res.principal.CanPublishTo("any", "site") {
		t.Fatal("global read grant should read every site and nothing more")
	}
}

func TestConsentRejectsInvalidGrants(t *testing.T) {
	s, _ := newTestService(t)
	tooMany := make([]consentGrant, maxConsentGrants+1)
	for i := range tooMany {
		tooMany[i] = grant("group", "claude", "publish")
	}
	for name, grants := range map[string][]consentGrant{
		"no grants":         nil,
		"empty permissions": {grant("group", "claude")},
		"unknown cap":       {grant("group", "claude", "publish", "frobnicate")},
		"blank cap":         {grant("group", "claude", " ")},
		"bad label":         {grant("group", "Bad_Label", "read")},
		"too deep":          {grant("group", "a/b/c/d/e/f", "read")},
		"site no target":    {grant("site", "", "read")},
		"unknown kind":      {grant("galaxy", "claude", "read")},
		"too many grants":   tooMany,
	} {
		res := approveConsent(t, s, grants, true)
		if res.errCode != "invalid_scope" || res.errDesc == "" {
			t.Errorf("%s: got %q / %q, want invalid_scope", name, res.errCode, res.errDesc)
		}
	}
}

func TestConsentAcceptsMaxGrants(t *testing.T) {
	s, _ := newTestService(t)
	grants := make([]consentGrant, maxConsentGrants)
	for i := range grants {
		grants[i] = grant("group", "claude", "publish")
	}
	if res := approveConsent(t, s, grants, false); res.errCode != "" {
		t.Fatalf("unexpected error: %s: %s", res.errCode, res.errDesc)
	}
}

func TestConsentLogsOneLinePerGrant(t *testing.T) {
	s, _ := newTestService(t)
	var buf bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&buf, nil))
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	e.redirect(e.body("approve", []consentGrant{grant("group", "claude", "publish"), grant("site", "docs/app", "read")}, false))
	if n := strings.Count(buf.String(), "MCP consent granted"); n != 2 {
		t.Fatalf("logged %d consent lines, want 2:\n%s", n, buf.String())
	}
}

func TestConsentCodeIsSingleUseAndBoundToPKCE(t *testing.T) {
	s, _ := newTestService(t)
	e := newConsentEnv(t, s, &auth.Principal{User: "tester"})
	q := e.redirect(e.body("approve", []consentGrant{grant("group", "claude", "publish")}, false))
	code := q.Get("code")
	e.principalFor(code)
	if tryReuse(t, e.srv.URL, e.reg.ClientID, consentRedirectURI, code) {
		t.Fatal("authorization code was reusable")
	}
}
