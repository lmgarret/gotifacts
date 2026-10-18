package mcpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

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
		s.log.Error("failed to register OAuth client", "name", req.ClientName, "err", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not register client")
		return
	}
	s.log.Info("OAuth client registered", "client_id", clientID, "name", req.ClientName, "auth_method", method)

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
// the consent screen through to the approval.
type authParams struct {
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
}

// authError describes why an authorization request was rejected. trusted is
// true once the redirect URI has been verified against the client: from then on
// the error is reported to the client as an OAuth error (RFC 6749 §4.1.2.1);
// before that it is shown to the user directly, never redirected.
type authError struct {
	trusted bool
	code    string // OAuth error code; meaningful when trusted
	desc    string
}

func (e *authError) Error() string { return e.desc }

// checkAuthRequest validates the client, redirect URI, and PKCE parameters
// without writing a response.
func (s *Service) checkAuthRequest(ctx context.Context, responseType, clientID, redirectURI, challenge, method, state string) (authParams, *store.OAuthClient, *authError) {
	if clientID == "" {
		return authParams{}, nil, &authError{desc: "missing client_id"}
	}
	client, err := s.store.GetOAuthClient(ctx, clientID)
	if err != nil {
		return authParams{}, nil, &authError{desc: "unknown client_id"}
	}
	if redirectURI == "" || !slices.Contains(client.RedirectURIs, redirectURI) {
		return authParams{}, nil, &authError{desc: "invalid redirect_uri for client"}
	}
	// redirect_uri is now trusted; report subsequent errors via redirect.
	fail := func(code, desc string) (authParams, *store.OAuthClient, *authError) {
		return authParams{}, nil, &authError{trusted: true, code: code, desc: desc}
	}
	if responseType != "code" {
		return fail("unsupported_response_type", "only response_type=code is supported")
	}
	if challenge == "" {
		return fail("invalid_request", "code_challenge is required (PKCE)")
	}
	if method == "" {
		method = "S256"
	}
	if method != "S256" {
		return fail("invalid_request", "code_challenge_method must be S256")
	}
	return authParams{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		State:               state,
	}, client, nil
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
	s.log.Info("MCP connection established", "user", ac.User, "client_id", clientID, "conn", connID)
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
	u, ok := errorRedirectURL(redirectURI, state, code, desc)
	if !ok {
		http.Error(w, code+": "+desc, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

// errorRedirectURL builds the OAuth error redirect to the client (RFC 6749
// §4.1.2.1). It reports false when redirectURI does not parse.
func errorRedirectURL(redirectURI, state, code, desc string) (string, bool) {
	return redirectURL(redirectURI, state, url.Values{"error": {code}, "error_description": {desc}})
}

// redirectURL appends params (and the client's state, when set) to the query of
// redirectURI. It reports false when redirectURI does not parse.
func redirectURL(redirectURI, state string, params url.Values) (string, bool) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", false
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String(), true
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
