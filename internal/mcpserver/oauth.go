package mcpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// labelRE validates a single group/site path label for consent grant targets.
var labelRE = regexp.MustCompile(router.LabelPattern)

// consentCapabilities is the fixed set of capabilities offered on the consent
// screen, in display order. publish is pre-checked.
var consentCapabilities = []keys.Capability{
	keys.CapPublish, keys.CapPatch, keys.CapUnpublish, keys.CapRollback,
}

// RegisterPublic registers the machine-facing OAuth + MCP routes. These MUST be
// served WITHOUT forward-auth — they authenticate via OAuth (PKCE, client creds,
// bearer token), not the proxy identity header.
func (s *Service) RegisterPublic(mux *http.ServeMux) {
	mux.Handle("/.well-known/oauth-protected-resource", s.protectedResourceMetadata())
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleASMetadata)
	mux.HandleFunc("/mcp/oauth/register", s.handleRegister)
	mux.HandleFunc("/mcp/oauth/token", s.handleToken)
	mux.Handle("/mcp", s.stream)
}

// --- discovery metadata -------------------------------------------------

func (s *Service) protectedResourceMetadata() http.Handler {
	return mcpauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               s.cfg.BaseURL() + "/mcp",
		AuthorizationServers:   []string{s.cfg.BaseURL()},
		ScopesSupported:        []string{scopePublish},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "gotifacts",
	})
}

func (s *Service) handleASMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := s.cfg.BaseURL()
	meta := oauthex.AuthServerMeta{
		Issuer:                            base,
		AuthorizationEndpoint:             base + "/mcp/oauth/authorize",
		TokenEndpoint:                     base + "/mcp/oauth/token",
		RegistrationEndpoint:              base + "/mcp/oauth/register",
		ScopesSupported:                   []string{scopePublish},
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		CodeChallengeMethodsSupported:     []string{"S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

// --- dynamic client registration (RFC 7591) -----------------------------

type registerRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.reg.allow() {
		writeOAuthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "registration rate limit exceeded")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON body")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri must be https or http://localhost")
			return
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}

	clientID, err := randID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate client id")
		return
	}

	var plainSecret, secretHash string
	if method == "client_secret_post" || method == "client_secret_basic" {
		plainSecret, secretHash, err = randToken()
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate client secret")
			return
		}
	}

	if err := s.store.CreateOAuthClient(r.Context(), store.OAuthClient{
		ClientID:        clientID,
		SecretHash:      secretHash,
		Name:            req.ClientName,
		RedirectURIs:    req.RedirectURIs,
		TokenAuthMethod: method,
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not register client")
		return
	}

	grants := req.GrantTypes
	if len(grants) == 0 {
		grants = []string{"authorization_code", "refresh_token"}
	}
	respTypes := req.ResponseTypes
	if len(respTypes) == 0 {
		respTypes = []string{"code"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(registerResponse{
		ClientID:                clientID,
		ClientSecret:            plainSecret,
		ClientIDIssuedAt:        time.Now().Unix(),
		RedirectURIs:            req.RedirectURIs,
		ClientName:              req.ClientName,
		TokenEndpointAuthMethod: method,
		GrantTypes:              grants,
		ResponseTypes:           respTypes,
	})
}

// --- authorization endpoint (forward-auth) ------------------------------

// authParams are the validated authorization-request parameters carried from
// the GET consent render through to the POST approval.
type authParams struct {
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
}

// consentCap is one capability checkbox on the consent form.
type consentCap struct {
	Value   string
	Checked bool
}

// consentData is the template model for the consent screen.
type consentData struct {
	ClientName          string
	BaseHost            string
	User                string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
	CSRF                string
	DefaultTarget       string
	Capabilities        []consentCap
}

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Authorize gotifacts connector</title>
<style>body{font-family:system-ui,sans-serif;max-width:34rem;margin:4rem auto;padding:0 1rem}
.card{border:1px solid #ddd;border-radius:8px;padding:1.5rem}button{font-size:1rem;padding:.5rem 1rem;border-radius:6px;border:0;cursor:pointer}
.approve{background:#2563eb;color:#fff}.deny{background:#eee;margin-left:.5rem}
fieldset{border:1px solid #ddd;border-radius:6px;margin:1rem 0}label{display:block;margin:.25rem 0}
input[type=text]{font-size:1rem;padding:.3rem;width:100%;box-sizing:border-box}</style></head>
<body><div class="card">
<h2>Authorize MCP connector</h2>
<p><strong>{{.ClientName}}</strong> wants to manage sites on your behalf, as
<strong>{{.User}}</strong>, on <code>{{.BaseHost}}</code>. Choose what it may do.</p>
<form method="post" action="/mcp/oauth/authorize">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<fieldset><legend>Scope</legend>
<label><input type="radio" name="target_kind" value="group" checked> Group subtree (and everything beneath it)</label>
<label><input type="radio" name="target_kind" value="site"> A single site (exact <code>group/slug</code>)</label>
<label>Target <input type="text" name="target" value="{{.DefaultTarget}}" placeholder="e.g. claude or claude/app"></label>
</fieldset>
<fieldset><legend>Capabilities</legend>
{{range .Capabilities}}<label><input type="checkbox" name="capability" value="{{.Value}}"{{if .Checked}} checked{{end}}> {{.Value}}</label>
{{end}}</fieldset>
<button class="approve" name="action" value="approve" type="submit">Approve</button>
<button class="deny" name="action" value="deny" type="submit">Deny</button>
</form></div></body></html>`))

// HandleAuthorize serves the OAuth consent screen (GET) and processes the
// approval (POST). It matches the api package's forward-auth handler signature,
// so the principal is the SSO-authenticated user. Access is gated to the MCP
// consent allowlist.
func (s *Service) HandleAuthorize(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.cfg.MCPUserAllowed(p.User) {
		http.Error(w, "you are not permitted to authorize MCP connectors on this instance", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		s.authorizeSubmit(w, r, p)
		return
	}

	q := r.URL.Query()
	ap, client, ok := s.validateAuthRequest(w, r, q.Get("response_type"), q.Get("client_id"), q.Get("redirect_uri"),
		q.Get("code_challenge"), q.Get("code_challenge_method"), q.Get("state"))
	if !ok {
		return
	}
	caps := make([]consentCap, len(consentCapabilities))
	for i, c := range consentCapabilities {
		caps[i] = consentCap{Value: string(c), Checked: c == keys.CapPublish}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentTmpl.Execute(w, consentData{
		ClientName:          clientDisplayName(client),
		BaseHost:            s.cfg.BaseDomain,
		User:                p.User,
		ClientID:            ap.ClientID,
		RedirectURI:         ap.RedirectURI,
		CodeChallenge:       ap.CodeChallenge,
		CodeChallengeMethod: ap.CodeChallengeMethod,
		State:               ap.State,
		CSRF:                s.csrfToken(p.User),
		DefaultTarget:       s.cfg.MCPGroup,
		Capabilities:        caps,
	})
}

func (s *Service) authorizeSubmit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !s.csrfValid(p.User, r.PostForm.Get("csrf")) {
		http.Error(w, "invalid CSRF token", http.StatusBadRequest)
		return
	}
	ap, _, ok := s.validateAuthRequest(w, r, "code", r.PostForm.Get("client_id"), r.PostForm.Get("redirect_uri"),
		r.PostForm.Get("code_challenge"), r.PostForm.Get("code_challenge_method"), r.PostForm.Get("state"))
	if !ok {
		return
	}
	if r.PostForm.Get("action") != "approve" {
		redirectWithError(w, r, ap.RedirectURI, ap.State, "access_denied", "user denied the request")
		return
	}

	grant, err := parseConsentGrant(r)
	if err != nil {
		redirectWithError(w, r, ap.RedirectURI, ap.State, "invalid_scope", err.Error())
		return
	}

	code, codeHash, err := randToken()
	if err != nil {
		redirectWithError(w, r, ap.RedirectURI, ap.State, "server_error", "could not issue code")
		return
	}
	if err := s.store.CreateAuthCode(r.Context(), store.AuthCode{
		Hash:                codeHash,
		ClientID:            ap.ClientID,
		User:                p.User,
		RedirectURI:         ap.RedirectURI,
		CodeChallenge:       ap.CodeChallenge,
		CodeChallengeMethod: ap.CodeChallengeMethod,
		Grants:              []store.Grant{grant},
		ExpiresAt:           time.Now().Add(codeTTL),
	}); err != nil {
		redirectWithError(w, r, ap.RedirectURI, ap.State, "server_error", "could not persist code")
		return
	}

	u, _ := url.Parse(ap.RedirectURI)
	qq := u.Query()
	qq.Set("code", code)
	if ap.State != "" {
		qq.Set("state", ap.State)
	}
	u.RawQuery = qq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// validateAuthRequest validates the client, redirect URI, and PKCE parameters.
// Errors before the redirect URI is trusted render an HTML page; once the
// redirect URI is verified against the client, later errors redirect back to
// the client with an OAuth error (per RFC 6749 §4.1.2.1).
func (s *Service) validateAuthRequest(w http.ResponseWriter, r *http.Request, responseType, clientID, redirectURI, challenge, method, state string) (authParams, *store.OAuthClient, bool) {
	if clientID == "" {
		http.Error(w, "missing client_id", http.StatusBadRequest)
		return authParams{}, nil, false
	}
	client, err := s.store.GetOAuthClient(r.Context(), clientID)
	if err != nil {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return authParams{}, nil, false
	}
	if redirectURI == "" || !slices.Contains(client.RedirectURIs, redirectURI) {
		http.Error(w, "invalid redirect_uri for client", http.StatusBadRequest)
		return authParams{}, nil, false
	}
	// redirect_uri is now trusted; report subsequent errors via redirect.
	if responseType != "code" {
		redirectWithError(w, r, redirectURI, state, "unsupported_response_type", "only response_type=code is supported")
		return authParams{}, nil, false
	}
	if challenge == "" {
		redirectWithError(w, r, redirectURI, state, "invalid_request", "code_challenge is required (PKCE)")
		return authParams{}, nil, false
	}
	if method == "" {
		method = "S256"
	}
	if method != "S256" {
		redirectWithError(w, r, redirectURI, state, "invalid_request", "code_challenge_method must be S256")
		return authParams{}, nil, false
	}
	return authParams{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		State:               state,
	}, client, true
}

// --- token endpoint -----------------------------------------------------

func (s *Service) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.grantAuthorizationCode(w, r)
	case "refresh_token":
		s.grantRefreshToken(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (s *Service) grantAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	clientID, ok := s.authenticateClient(w, r)
	if !ok {
		return
	}
	code := r.PostForm.Get("code")
	verifier := r.PostForm.Get("code_verifier")
	if code == "" || verifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code and code_verifier are required")
		return
	}
	ac, err := s.store.ConsumeAuthCode(r.Context(), keys.Hash(code))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if ac.ClientID != clientID || ac.RedirectURI != r.PostForm.Get("redirect_uri") {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id/redirect_uri mismatch")
		return
	}
	if !pkceVerify(verifier, ac.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	connID, err := randConnID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue token")
		return
	}
	s.issueTokens(w, r, connID, clientID, ac.User, ac.Grants)
}

func (s *Service) grantRefreshToken(w http.ResponseWriter, r *http.Request) {
	clientID, ok := s.authenticateClient(w, r)
	if !ok {
		return
	}
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	rec, err := s.store.FindToken(r.Context(), "refresh", keys.Hash(refresh))
	if err != nil || rec.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	// Rotate: invalidate the presented refresh token before minting new ones,
	// reusing the connection id so the connection survives the rotation.
	_ = s.store.DeleteToken(r.Context(), rec.Hash)
	s.issueTokens(w, r, rec.ConnID, clientID, rec.User, rec.Grants)
}

func (s *Service) issueTokens(w http.ResponseWriter, r *http.Request, connID, clientID, user string, grants []store.Grant) {
	access, accessHash, err := randToken()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue token")
		return
	}
	refresh, refreshHash, err := randToken()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue token")
		return
	}
	now := time.Now()
	if err := s.store.CreateToken(r.Context(), store.Token{
		Hash: accessHash, ConnID: connID, Kind: "access", ClientID: clientID, User: user,
		Grants: grants, ExpiresAt: now.Add(s.cfg.MCPAccessTokenTTL),
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist token")
		return
	}
	if err := s.store.CreateToken(r.Context(), store.Token{
		Hash: refreshHash, ConnID: connID, Kind: "refresh", ClientID: clientID, User: user,
		Grants: grants, ExpiresAt: now.Add(s.cfg.MCPRefreshTokenTTL),
	}); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist token")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.cfg.MCPAccessTokenTTL.Seconds()),
		RefreshToken: refresh,
		Scope:        scopeFromGrants(grants),
	})
}

// parseConsentGrant builds the single grant the approving user selected on the
// consent form (target kind + target path + capabilities).
func parseConsentGrant(r *http.Request) (store.Grant, error) {
	kind := store.ParseGrantKind(r.PostForm.Get("target_kind"))
	target := normalizeTarget(r.PostForm.Get("target"))
	caps, err := keys.ParseCapabilities(strings.Join(r.PostForm["capability"], ","))
	if err != nil {
		return store.Grant{}, errors.New("select at least one capability")
	}
	if err := validateGrantTarget(kind, target); err != nil {
		return store.Grant{}, err
	}
	return store.Grant{Kind: kind, Target: target, Permissions: caps}, nil
}

func normalizeTarget(t string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(t)), "/")
}

// validateGrantTarget rejects malformed targets. A group target may be empty
// (global); a site target must name a concrete site.
func validateGrantTarget(kind store.GrantKind, target string) error {
	if target == "" {
		if kind == store.GrantSite {
			return errors.New("a site target is required")
		}
		return nil
	}
	segs := strings.Split(target, "/")
	if len(segs) > router.MaxDepth {
		return errors.New("target is too deep")
	}
	for _, seg := range segs {
		if !labelRE.MatchString(seg) {
			return errors.New("target contains an invalid label")
		}
	}
	return nil
}

// scopeFromGrants renders the union of granted capabilities as an OAuth scope
// string, for the token response (informational).
func scopeFromGrants(grants []store.Grant) string {
	seen := map[keys.Capability]bool{}
	var caps []keys.Capability
	for _, g := range grants {
		for _, c := range g.Permissions {
			if !seen[c] {
				seen[c] = true
				caps = append(caps, c)
			}
		}
	}
	return keys.JoinCapabilities(caps)
}

// authenticateClient resolves the client_id and verifies a client secret if the
// client is confidential. Public (PKCE-only) clients authenticate with just the
// client_id; PKCE protects the code exchange.
func (s *Service) authenticateClient(w http.ResponseWriter, r *http.Request) (string, bool) {
	clientID := r.PostForm.Get("client_id")
	secret := r.PostForm.Get("client_secret")
	if u, p, ok := r.BasicAuth(); ok {
		clientID, secret = u, p
	}
	if clientID == "" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client_id is required")
		return "", false
	}
	client, err := s.store.GetOAuthClient(r.Context(), clientID)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return "", false
	}
	if client.SecretHash != "" {
		if secret == "" || !keys.Equal(keys.Hash(secret), client.SecretHash) {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "invalid client secret")
			return "", false
		}
	}
	return clientID, true
}

// --- helpers ------------------------------------------------------------

func (s *Service) csrfToken(user string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte(user))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) csrfValid(user, token string) bool {
	expected := s.csrfToken(user)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

func pkceVerify(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return false
}

func clientDisplayName(c *store.OAuthClient) string {
	if c != nil && strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return "An MCP client"
}

func redirectWithError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, code+": "+desc, http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", desc)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}

// rateLimiter is a simple fixed-window limiter guarding client registration.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	count  int
	reset  time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window}
}

func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if now.After(rl.reset) {
		rl.count = 0
		rl.reset = now.Add(rl.window)
	}
	if rl.count >= rl.limit {
		return false
	}
	rl.count++
	return true
}
