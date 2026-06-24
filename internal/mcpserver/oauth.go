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
	keys.CapPublish, keys.CapPatch, keys.CapUnpublish, keys.CapRollback, keys.CapPurge,
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
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Authorize — gotifacts</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
<style>
:root{--bg:#f9fafb;--surface:#ffffff;--border:#e4e4e7;--text:#18181b;--muted:#71717a;--accent:#1a7f3c;--accent-strong:#16a34a;--accent-soft:#d4f3df;--radius:10px;--shadow:0 1px 3px rgba(0,0,0,.08)}
@media(prefers-color-scheme:dark){:root{--bg:#17181c;--surface:#23262f;--border:#374151;--text:#f9fafb;--muted:#9ca3af;--accent:#6ee79b;--accent-strong:#208a43;--accent-soft:#11351f;--shadow:0 1px 3px rgba(0,0,0,.4)}}
*{box-sizing:border-box}
body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--text);line-height:1.5;min-height:100vh;display:flex;flex-direction:column}
.logo-light{display:flex;justify-content:center;margin-bottom:1.75rem}.logo-dark{display:none}
@media(prefers-color-scheme:dark){.logo-light{display:none}.logo-dark{display:flex;justify-content:center;margin-bottom:1.75rem}}
.logo-light svg,.logo-dark svg{height:2.25rem;width:auto}
main{flex:1;display:flex;align-items:flex-start;justify-content:center;padding:3rem 1rem}
.card{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);box-shadow:var(--shadow);padding:2rem;width:100%;max-width:30rem}
.user-chip{display:inline-flex;align-items:center;gap:.4rem;background:var(--accent-soft);border:1px solid color-mix(in srgb,var(--accent) 30%,transparent);border-radius:999px;padding:.2rem .65rem .2rem .45rem;font-size:.85rem;margin-bottom:1.25rem}
.user-dot{width:.5rem;height:.5rem;border-radius:50%;background:var(--accent);flex-shrink:0}
.card h2{margin:0 0 .35rem;font-size:1.15rem}
.subtitle{margin:0 0 1.5rem;color:var(--muted);font-size:.9rem}
.subtitle strong{color:var(--text)}
fieldset{border:1px solid var(--border);border-radius:8px;margin:0 0 1rem;padding:.7rem 1rem;background:var(--bg)}
legend{font-weight:600;font-size:.85rem;padding:0 .3rem;color:var(--muted)}
label{display:flex;align-items:baseline;gap:.45rem;margin:.3rem 0;font-size:.9rem}
input[type=radio],input[type=checkbox]{accent-color:var(--accent);flex-shrink:0;margin-top:.15rem}
input[type=text]{font:inherit;padding:.4rem .6rem;border:1px solid var(--border);border-radius:8px;background:var(--surface);color:var(--text);width:100%;margin-top:.5rem;display:block}
input[type=text]:focus{outline:1px solid var(--accent);border-color:var(--accent)}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em}
.actions{display:flex;gap:.5rem;margin-top:1.5rem;flex-wrap:wrap}
button{font:inherit;cursor:pointer;border-radius:8px;padding:.5rem 1.1rem;font-size:.95rem;transition:background .15s,border-color .15s}
.approve{background:var(--accent);color:#fff;border:1px solid var(--accent);font-weight:600}
.approve:hover{background:var(--accent-strong);border-color:var(--accent-strong)}
.deny{background:var(--surface);color:var(--text);border:1px solid var(--border)}
.deny:hover{border-color:var(--accent)}
</style>
</head>
<body>
<main>
  <div class="card">
    <div class="logo-light"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 264 64" role="img" aria-label="gotifacts"><g fill="none" stroke="#16a34a" stroke-width="3.6" stroke-linejoin="round" stroke-linecap="round"><path d="M55 13 L9 31 L28 37 L34 53 L40 39 Z"/><path d="M55 13 L28 37"/></g><path transform="translate(78.20 43.28)" d="M12.80 9.32Q8.55 9.32 5.96 7.70Q3.37 6.08 2.77 3.07L8.81 2.36Q9.13 3.76 10.19 4.55Q11.26 5.35 12.98 5.35Q15.49 5.35 16.65 3.80Q17.81 2.26 17.81-0.79L17.81-2.02L17.85-4.32L17.81-4.32Q15.81-0.04 10.33-0.04Q6.27-0.04 4.04-3.09Q1.80-6.14 1.80-11.82Q1.80-17.51 4.10-20.60Q6.40-23.70 10.79-23.70Q15.86-23.70 17.81-19.51L17.92-19.51Q17.92-20.26 18.01-21.55Q18.11-22.84 18.22-23.25L23.93-23.25Q23.80-20.93 23.80-17.87L23.80-0.71Q23.80 4.25 20.99 6.79Q18.18 9.32 12.80 9.32M17.85-11.95Q17.85-15.53 16.58-17.54Q15.30-19.55 12.93-19.55Q8.10-19.55 8.10-11.82Q8.10-4.23 12.89-4.23Q15.30-4.23 16.58-6.24Q17.85-8.25 17.85-11.95M52.04-11.64Q52.04-5.99 48.90-2.78Q45.76 0.43 40.22 0.43Q34.78 0.43 31.69-2.79Q28.60-6.02 28.60-11.64Q28.60-17.25 31.69-20.46Q34.78-23.68 40.35-23.68Q46.04-23.68 49.04-20.57Q52.04-17.47 52.04-11.64M45.72-11.64Q45.72-15.79 44.37-17.66Q43.01-19.53 40.43-19.53Q34.93-19.53 34.93-11.64Q34.93-7.76 36.28-5.73Q37.62-3.70 40.15-3.70Q45.72-3.70 45.72-11.64M62.78 0.39Q60.11 0.39 58.67-1.06Q57.23-2.51 57.23-5.46L57.23-19.16L54.29-19.16L54.29-23.25L57.54-23.25L59.43-28.70L63.21-28.70L63.21-23.25L67.61-23.25L67.61-19.16L63.21-19.16L63.21-7.09Q63.21-5.39 63.85-4.59Q64.50-3.78 65.85-3.78Q66.56-3.78 67.87-4.08L67.87-0.34Q65.63 0.39 62.78 0.39M77.52-27.44L71.48-27.44L71.48-31.88L77.52-31.88L77.52-27.44M77.52 0L71.48 0L71.48-23.25L77.52-23.25L77.52 0M95.37-19.16L90.79-19.16L90.79 0L84.78 0L84.78-19.16L81.38-19.16L81.38-23.25L84.78-23.25L84.78-25.67Q84.78-28.83 86.45-30.36Q88.13-31.88 91.54-31.88Q93.24-31.88 95.37-31.54L95.37-27.65Q94.49-27.84 93.61-27.84Q92.06-27.84 91.43-27.23Q90.79-26.62 90.79-25.07L90.79-23.25L95.37-23.25L95.37-19.16M103.73 0.43Q100.35 0.43 98.46-1.41Q96.57-3.24 96.57-6.57Q96.57-10.18 98.92-12.07Q101.28-13.96 105.75-14.01L110.75-14.09L110.75-15.28Q110.75-17.55 109.96-18.66Q109.16-19.77 107.36-19.77Q105.68-19.77 104.90-19Q104.11-18.24 103.92-16.48L97.63-16.78Q98.21-20.17 100.73-21.92Q103.25-23.68 107.62-23.68Q112.02-23.68 114.40-21.51Q116.79-19.34 116.79-15.34L116.79-6.87Q116.79-4.92 117.23-4.18Q117.67-3.44 118.70-3.44Q119.39-3.44 120.03-3.57L120.03-0.30Q119.50-0.17 119.07-0.06Q118.64 0.04 118.21 0.11Q117.78 0.17 117.29 0.21Q116.81 0.26 116.17 0.26Q113.89 0.26 112.80-0.86Q111.72-1.98 111.50-4.15L111.38-4.15Q108.84 0.43 103.73 0.43M110.75-9.58L110.75-10.76L107.66-10.72Q105.55-10.63 104.67-10.26Q103.79-9.88 103.33-9.11Q102.87-8.34 102.87-7.05Q102.87-5.39 103.63-4.59Q104.39-3.78 105.66-3.78Q107.08-3.78 108.25-4.55Q109.42-5.33 110.09-6.69Q110.75-8.06 110.75-9.58M132.52 0.43Q127.23 0.43 124.35-2.72Q121.47-5.87 121.47-11.49Q121.47-17.25 124.37-20.46Q127.27-23.68 132.60-23.68Q136.71-23.68 139.39-21.61Q142.08-19.55 142.76-15.92L136.68-15.62Q136.43-17.40 135.39-18.47Q134.36-19.53 132.47-19.53Q127.81-19.53 127.81-11.73Q127.81-3.70 132.56-3.70Q134.28-3.70 135.44-4.78Q136.60-5.87 136.88-8.01L142.94-7.73Q142.61-5.35 141.23-3.48Q139.84-1.61 137.59-0.59Q135.33 0.43 132.52 0.43M153.25 0.39Q150.58 0.39 149.14-1.06Q147.71-2.51 147.71-5.46L147.71-19.16L144.76-19.16L144.76-23.25L148.01-23.25L149.90-28.70L153.68-28.70L153.68-23.25L158.08-23.25L158.08-19.16L153.68-19.16L153.68-7.09Q153.68-5.39 154.32-4.59Q154.97-3.78 156.32-3.78Q157.03-3.78 158.34-4.08L158.34-0.34Q156.11 0.39 153.25 0.39M181.54-6.79Q181.54-3.42 178.78-1.49Q176.02 0.43 171.14 0.43Q166.35 0.43 163.81-1.08Q161.26-2.60 160.42-5.80L165.73-6.60Q166.18-4.94 167.29-4.25Q168.39-3.57 171.14-3.57Q173.68-3.57 174.84-4.21Q176-4.86 176-6.23Q176-7.35 175.07-8Q174.13-8.66 171.90-9.11Q166.78-10.12 165-10.99Q163.22-11.86 162.28-13.25Q161.35-14.63 161.35-16.65Q161.35-19.98 163.92-21.84Q166.48-23.70 171.19-23.70Q175.33-23.70 177.86-22.09Q180.38-20.47 181.01-17.42L175.66-16.87Q175.40-18.28 174.39-18.98Q173.38-19.68 171.19-19.68Q169.04-19.68 167.96-19.13Q166.89-18.58 166.89-17.29Q166.89-16.29 167.72-15.69Q168.54-15.10 170.50-14.72Q173.23-14.16 175.34-13.57Q177.46-12.98 178.74-12.16Q180.02-11.34 180.78-10.07Q181.54-8.79 181.54-6.79" fill="#14532d"/></svg></div>
    <div class="logo-dark"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 264 64" role="img" aria-label="gotifacts"><g fill="none" stroke="#16a34a" stroke-width="3.6" stroke-linejoin="round" stroke-linecap="round"><path d="M55 13 L9 31 L28 37 L34 53 L40 39 Z"/><path d="M55 13 L28 37"/></g><path transform="translate(78.20 43.28)" d="M12.80 9.32Q8.55 9.32 5.96 7.70Q3.37 6.08 2.77 3.07L8.81 2.36Q9.13 3.76 10.19 4.55Q11.26 5.35 12.98 5.35Q15.49 5.35 16.65 3.80Q17.81 2.26 17.81-0.79L17.81-2.02L17.85-4.32L17.81-4.32Q15.81-0.04 10.33-0.04Q6.27-0.04 4.04-3.09Q1.80-6.14 1.80-11.82Q1.80-17.51 4.10-20.60Q6.40-23.70 10.79-23.70Q15.86-23.70 17.81-19.51L17.92-19.51Q17.92-20.26 18.01-21.55Q18.11-22.84 18.22-23.25L23.93-23.25Q23.80-20.93 23.80-17.87L23.80-0.71Q23.80 4.25 20.99 6.79Q18.18 9.32 12.80 9.32M17.85-11.95Q17.85-15.53 16.58-17.54Q15.30-19.55 12.93-19.55Q8.10-19.55 8.10-11.82Q8.10-4.23 12.89-4.23Q15.30-4.23 16.58-6.24Q17.85-8.25 17.85-11.95M52.04-11.64Q52.04-5.99 48.90-2.78Q45.76 0.43 40.22 0.43Q34.78 0.43 31.69-2.79Q28.60-6.02 28.60-11.64Q28.60-17.25 31.69-20.46Q34.78-23.68 40.35-23.68Q46.04-23.68 49.04-20.57Q52.04-17.47 52.04-11.64M45.72-11.64Q45.72-15.79 44.37-17.66Q43.01-19.53 40.43-19.53Q34.93-19.53 34.93-11.64Q34.93-7.76 36.28-5.73Q37.62-3.70 40.15-3.70Q45.72-3.70 45.72-11.64M62.78 0.39Q60.11 0.39 58.67-1.06Q57.23-2.51 57.23-5.46L57.23-19.16L54.29-19.16L54.29-23.25L57.54-23.25L59.43-28.70L63.21-28.70L63.21-23.25L67.61-23.25L67.61-19.16L63.21-19.16L63.21-7.09Q63.21-5.39 63.85-4.59Q64.50-3.78 65.85-3.78Q66.56-3.78 67.87-4.08L67.87-0.34Q65.63 0.39 62.78 0.39M77.52-27.44L71.48-27.44L71.48-31.88L77.52-31.88L77.52-27.44M77.52 0L71.48 0L71.48-23.25L77.52-23.25L77.52 0M95.37-19.16L90.79-19.16L90.79 0L84.78 0L84.78-19.16L81.38-19.16L81.38-23.25L84.78-23.25L84.78-25.67Q84.78-28.83 86.45-30.36Q88.13-31.88 91.54-31.88Q93.24-31.88 95.37-31.54L95.37-27.65Q94.49-27.84 93.61-27.84Q92.06-27.84 91.43-27.23Q90.79-26.62 90.79-25.07L90.79-23.25L95.37-23.25L95.37-19.16M103.73 0.43Q100.35 0.43 98.46-1.41Q96.57-3.24 96.57-6.57Q96.57-10.18 98.92-12.07Q101.28-13.96 105.75-14.01L110.75-14.09L110.75-15.28Q110.75-17.55 109.96-18.66Q109.16-19.77 107.36-19.77Q105.68-19.77 104.90-19Q104.11-18.24 103.92-16.48L97.63-16.78Q98.21-20.17 100.73-21.92Q103.25-23.68 107.62-23.68Q112.02-23.68 114.40-21.51Q116.79-19.34 116.79-15.34L116.79-6.87Q116.79-4.92 117.23-4.18Q117.67-3.44 118.70-3.44Q119.39-3.44 120.03-3.57L120.03-0.30Q119.50-0.17 119.07-0.06Q118.64 0.04 118.21 0.11Q117.78 0.17 117.29 0.21Q116.81 0.26 116.17 0.26Q113.89 0.26 112.80-0.86Q111.72-1.98 111.50-4.15L111.38-4.15Q108.84 0.43 103.73 0.43M110.75-9.58L110.75-10.76L107.66-10.72Q105.55-10.63 104.67-10.26Q103.79-9.88 103.33-9.11Q102.87-8.34 102.87-7.05Q102.87-5.39 103.63-4.59Q104.39-3.78 105.66-3.78Q107.08-3.78 108.25-4.55Q109.42-5.33 110.09-6.69Q110.75-8.06 110.75-9.58M132.52 0.43Q127.23 0.43 124.35-2.72Q121.47-5.87 121.47-11.49Q121.47-17.25 124.37-20.46Q127.27-23.68 132.60-23.68Q136.71-23.68 139.39-21.61Q142.08-19.55 142.76-15.92L136.68-15.62Q136.43-17.40 135.39-18.47Q134.36-19.53 132.47-19.53Q127.81-19.53 127.81-11.73Q127.81-3.70 132.56-3.70Q134.28-3.70 135.44-4.78Q136.60-5.87 136.88-8.01L142.94-7.73Q142.61-5.35 141.23-3.48Q139.84-1.61 137.59-0.59Q135.33 0.43 132.52 0.43M153.25 0.39Q150.58 0.39 149.14-1.06Q147.71-2.51 147.71-5.46L147.71-19.16L144.76-19.16L144.76-23.25L148.01-23.25L149.90-28.70L153.68-28.70L153.68-23.25L158.08-23.25L158.08-19.16L153.68-19.16L153.68-7.09Q153.68-5.39 154.32-4.59Q154.97-3.78 156.32-3.78Q157.03-3.78 158.34-4.08L158.34-0.34Q156.11 0.39 153.25 0.39M181.54-6.79Q181.54-3.42 178.78-1.49Q176.02 0.43 171.14 0.43Q166.35 0.43 163.81-1.08Q161.26-2.60 160.42-5.80L165.73-6.60Q166.18-4.94 167.29-4.25Q168.39-3.57 171.14-3.57Q173.68-3.57 174.84-4.21Q176-4.86 176-6.23Q176-7.35 175.07-8Q174.13-8.66 171.90-9.11Q166.78-10.12 165-10.99Q163.22-11.86 162.28-13.25Q161.35-14.63 161.35-16.65Q161.35-19.98 163.92-21.84Q166.48-23.70 171.19-23.70Q175.33-23.70 177.86-22.09Q180.38-20.47 181.01-17.42L175.66-16.87Q175.40-18.28 174.39-18.98Q173.38-19.68 171.19-19.68Q169.04-19.68 167.96-19.13Q166.89-18.58 166.89-17.29Q166.89-16.29 167.72-15.69Q168.54-15.10 170.50-14.72Q173.23-14.16 175.34-13.57Q177.46-12.98 178.74-12.16Q180.02-11.34 180.78-10.07Q181.54-8.79 181.54-6.79" fill="#ffffff"/></svg></div>
    <div class="user-chip"><span class="user-dot"></span>{{.User}}</div>
    <h2>Authorize MCP Connector</h2>
    <p class="subtitle"><strong>{{.ClientName}}</strong> wants to manage sites on <code>{{.BaseHost}}</code>. Choose what it may do.</p>
    <form method="post" action="/mcp/oauth/authorize">
      <input type="hidden" name="client_id" value="{{.ClientID}}">
      <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
      <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
      <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
      <input type="hidden" name="state" value="{{.State}}">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <fieldset>
        <legend>Scope</legend>
        <label><input type="radio" name="target_kind" value="group" checked> Group subtree (and everything beneath it)</label>
        <label><input type="radio" name="target_kind" value="site"> A single site (exact <code>group/slug</code>)</label>
        <label style="flex-direction:column;align-items:stretch">Target<input type="text" name="target" value="{{.DefaultTarget}}" placeholder="e.g. claude or claude/app"></label>
      </fieldset>
      <fieldset>
        <legend>Capabilities</legend>
        {{range .Capabilities}}<label><input type="checkbox" name="capability" value="{{.Value}}"{{if .Checked}} checked{{end}}> <code>{{.Value}}</code></label>
        {{end}}
      </fieldset>
      <div class="actions">
        <button class="approve" name="action" value="approve" type="submit">Approve</button>
        <button class="deny" name="action" value="deny" type="submit">Deny</button>
      </div>
    </form>
  </div>
</main>
</body></html>`))

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
		s.log.Info("MCP consent denied", "user", p.User, "client_id", ap.ClientID)
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
		s.log.Error("failed to persist authorization code", "user", p.User, "client_id", ap.ClientID, "err", err)
		redirectWithError(w, r, ap.RedirectURI, ap.State, "server_error", "could not persist code")
		return
	}
	s.log.Info("MCP consent granted", "user", p.User, "client_id", ap.ClientID,
		"target_kind", grant.Kind, "target", grant.Target, "capabilities", keys.JoinCapabilities(grant.Permissions))

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
