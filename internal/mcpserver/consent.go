package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

// The consent screen is rendered by the portal SPA at /mcp/oauth/authorize. The
// SPA reads the OAuth parameters from its query string, loads the screen's
// data from GET /api/mcp/consent and submits the user's decision to
// POST /api/mcp/consent, which answers with the URL to send the browser to.

// maxConsentGrants bounds how many grants one consent can carry.
const maxConsentGrants = 16

// labelRE validates a single group/site path label for consent grant targets.
var labelRE = regexp.MustCompile(router.LabelPattern)

// consentCapabilities is the fixed set of capabilities offered on the consent
// screen, in display order (the same order as the API keys screen). The SPA
// pre-checks publish.
var consentCapabilities = []keys.Capability{
	keys.CapPublish, keys.CapUnpublish, keys.CapRollback, keys.CapPatch, keys.CapPurge, keys.CapRead,
}

// HandleAuthorize serves GET /mcp/oauth/authorize. It matches the api package's
// forward-auth handler shape, so the principal is the SSO-authenticated user.
// Access is gated to the MCP consent allowlist. The authorization request is
// validated first: errors before the redirect URI is trusted are shown
// directly, later ones are redirected to the client as OAuth errors. A valid
// request is served the portal SPA (spa), which renders the consent screen.
func (s *Service) HandleAuthorize(w http.ResponseWriter, r *http.Request, p *auth.Principal, spa http.Handler) {
	if !s.cfg.MCPUserAllowed(p.User) {
		http.Error(w, "you are not permitted to authorize MCP connectors on this instance", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	_, _, aerr := s.checkAuthRequest(r.Context(), q.Get("response_type"), q.Get("client_id"), q.Get("redirect_uri"),
		q.Get("code_challenge"), q.Get("code_challenge_method"), q.Get("state"))
	if aerr != nil {
		if aerr.trusted {
			redirectWithError(w, r, q.Get("redirect_uri"), q.Get("state"), aerr.code, aerr.desc)
		} else {
			http.Error(w, aerr.desc, http.StatusBadRequest)
		}
		return
	}
	spa.ServeHTTP(w, r)
}

// consentInfo is the JSON model of the consent screen.
type consentInfo struct {
	ClientName    string            `json:"client_name"`
	User          string            `json:"user"`
	BaseDomain    string            `json:"base_domain"`
	DefaultTarget string            `json:"default_target"`
	Capabilities  []keys.Capability `json:"capabilities"`
	CSRF          string            `json:"csrf"`
}

// HandleConsentInfo serves GET /api/mcp/consent: it validates the authorization
// request in the query string and returns what the consent screen needs. It
// never redirects; every failure is a JSON error.
func (s *Service) HandleConsentInfo(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.consentAllowed(w, p) {
		return
	}
	q := r.URL.Query()
	_, client, aerr := s.checkAuthRequest(r.Context(), q.Get("response_type"), q.Get("client_id"), q.Get("redirect_uri"),
		q.Get("code_challenge"), q.Get("code_challenge_method"), q.Get("state"))
	if aerr != nil {
		writeConsentError(w, http.StatusBadRequest, aerr.desc)
		return
	}
	writeConsentJSON(w, http.StatusOK, consentInfo{
		ClientName:    clientDisplayName(client),
		User:          p.User,
		BaseDomain:    s.cfg.BaseDomain,
		DefaultTarget: s.cfg.MCPGroup,
		Capabilities:  consentCapabilities,
		CSRF:          s.csrfToken(p.User),
	})
}

// consentGrant is one grant as submitted by the consent screen. An empty target
// on a group grant means "all sites".
type consentGrant struct {
	Kind        string   `json:"kind"`
	Target      string   `json:"target"`
	Permissions []string `json:"permissions"`
}

// consentRequest is the body of POST /api/mcp/consent.
type consentRequest struct {
	ClientID            string         `json:"client_id"`
	RedirectURI         string         `json:"redirect_uri"`
	CodeChallenge       string         `json:"code_challenge"`
	CodeChallengeMethod string         `json:"code_challenge_method"`
	State               string         `json:"state"`
	CSRF                string         `json:"csrf"`
	Action              string         `json:"action"` // "approve" | "deny"
	Grants              []consentGrant `json:"grants"`
	AcknowledgeGlobal   bool           `json:"acknowledge_global"`
}

// HandleConsentSubmit serves POST /api/mcp/consent. It checks the CSRF token,
// re-validates the authorization request and answers 200 {redirect_url}: the
// client's redirect URI carrying either a code (approve) or an OAuth error.
// Errors before the redirect URI is trusted are 400 JSON instead.
func (s *Service) HandleConsentSubmit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.consentAllowed(w, p) {
		return
	}
	var req consentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeConsentError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !s.csrfValid(p.User, req.CSRF) {
		writeConsentError(w, http.StatusBadRequest, "invalid CSRF token")
		return
	}
	if req.Action != "approve" && req.Action != "deny" {
		writeConsentError(w, http.StatusBadRequest, "action must be approve or deny")
		return
	}
	ap, _, aerr := s.checkAuthRequest(r.Context(), "code", req.ClientID, req.RedirectURI,
		req.CodeChallenge, req.CodeChallengeMethod, req.State)
	if aerr != nil {
		if aerr.trusted {
			s.respondRedirect(w, req.RedirectURI, req.State, aerr.code, aerr.desc)
		} else {
			writeConsentError(w, http.StatusBadRequest, aerr.desc)
		}
		return
	}
	if req.Action == "deny" {
		s.log.Info("MCP consent denied", "user", p.User, "client_id", ap.ClientID)
		s.respondRedirect(w, ap.RedirectURI, ap.State, "access_denied", "user denied the request")
		return
	}
	grants, err := parseConsentGrants(req.Grants, req.AcknowledgeGlobal)
	if err != nil {
		s.respondRedirect(w, ap.RedirectURI, ap.State, "invalid_scope", err.Error())
		return
	}
	s.issueCode(r.Context(), w, p, ap, grants)
}

// issueCode persists an authorization code for the approved grants and answers
// with the redirect that carries it.
func (s *Service) issueCode(ctx context.Context, w http.ResponseWriter, p *auth.Principal, ap authParams, grants []store.Grant) {
	code, codeHash, err := randToken()
	if err != nil {
		s.respondRedirect(w, ap.RedirectURI, ap.State, "server_error", "could not issue code")
		return
	}
	if err := s.store.CreateAuthCode(ctx, store.AuthCode{
		Hash:                codeHash,
		ClientID:            ap.ClientID,
		User:                p.User,
		RedirectURI:         ap.RedirectURI,
		CodeChallenge:       ap.CodeChallenge,
		CodeChallengeMethod: ap.CodeChallengeMethod,
		Grants:              grants,
		ExpiresAt:           time.Now().Add(codeTTL),
	}); err != nil {
		s.log.Error("failed to persist authorization code", "user", p.User, "client_id", ap.ClientID, "err", err)
		s.respondRedirect(w, ap.RedirectURI, ap.State, "server_error", "could not persist code")
		return
	}
	for _, g := range grants {
		s.log.Info("MCP consent granted", "user", p.User, "client_id", ap.ClientID,
			"target_kind", g.Kind, "target", g.Target, "capabilities", keys.JoinCapabilities(g.Permissions))
	}
	u, ok := redirectURL(ap.RedirectURI, ap.State, url.Values{"code": {code}})
	if !ok {
		writeConsentError(w, http.StatusBadRequest, "invalid redirect_uri for client")
		return
	}
	writeConsentJSON(w, http.StatusOK, map[string]string{"redirect_url": u})
}

// respondRedirect answers a consent submission with the client redirect that
// reports an OAuth error.
func (s *Service) respondRedirect(w http.ResponseWriter, redirectURI, state, code, desc string) {
	u, ok := errorRedirectURL(redirectURI, state, code, desc)
	if !ok {
		writeConsentError(w, http.StatusBadRequest, code+": "+desc)
		return
	}
	writeConsentJSON(w, http.StatusOK, map[string]string{"redirect_url": u})
}

// consentAllowed gates the consent endpoints to the MCP consent allowlist,
// answering 403 JSON otherwise.
func (s *Service) consentAllowed(w http.ResponseWriter, p *auth.Principal) bool {
	if s.cfg.MCPUserAllowed(p.User) {
		return true
	}
	writeConsentError(w, http.StatusForbidden, "you are not permitted to authorize MCP connectors on this instance")
	return false
}

func writeConsentJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeConsentError(w http.ResponseWriter, status int, msg string) {
	writeConsentJSON(w, status, map[string]string{"error": msg})
}

// parseConsentGrants validates and normalises the grants the approving user
// chose. It is authoritative: the SPA's checks are only a convenience. A global
// grant (group kind, empty target) needs the risk acknowledgement.
func parseConsentGrants(in []consentGrant, ackGlobal bool) ([]store.Grant, error) {
	if len(in) == 0 {
		return nil, errors.New("select at least one grant")
	}
	if len(in) > maxConsentGrants {
		return nil, errors.New("too many grants")
	}
	grants := make([]store.Grant, 0, len(in))
	for _, g := range in {
		grant, err := parseConsentGrant(g)
		if err != nil {
			return nil, err
		}
		if grant.Kind == store.GrantGroup && grant.Target == "" && !ackGlobal {
			return nil, errors.New("acknowledge the global-scope risk")
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

func parseConsentGrant(g consentGrant) (store.Grant, error) {
	kind := store.GrantKind(g.Kind)
	if kind != store.GrantGroup && kind != store.GrantSite {
		return store.Grant{}, errors.New("grant kind must be group or site")
	}
	target := normalizeTarget(g.Target)
	if err := validateGrantTarget(kind, target); err != nil {
		return store.Grant{}, err
	}
	caps, err := parseConsentCaps(g.Permissions)
	if err != nil {
		return store.Grant{}, err
	}
	return store.Grant{Kind: kind, Target: target, Permissions: caps}, nil
}

// parseConsentCaps parses one grant's permissions: non-empty, deduplicated, and
// limited to the capabilities offered on the consent screen.
func parseConsentCaps(vals []string) ([]keys.Capability, error) {
	var caps []keys.Capability
	for _, v := range vals {
		c, err := keys.ParseCapability(v)
		if err != nil || !slices.Contains(consentCapabilities, c) {
			return nil, errors.New("invalid capability")
		}
		if !slices.Contains(caps, c) {
			caps = append(caps, c)
		}
	}
	if len(caps) == 0 {
		return nil, errors.New("each grant needs at least one permission")
	}
	return caps, nil
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
