// Package auth implements the two-plane authorization model.
//
//   - Management plane: identity asserted by a forward-auth header, honored ONLY
//     when the request's direct peer IP is within a trusted-proxy CIDR. The
//     principal is the header user; admin iff that user is in the allowlist.
//   - Ingest plane: identity asserted ONLY by a scoped API key carried in the
//     Authorization: Bearer header. The forward-auth header is ignored here.
//
// Header spoofing is the top security risk: a client-supplied identity header
// from an untrusted source is never trusted.
package auth

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

// Source identifies how a principal authenticated.
type Source string

// Authentication sources.
const (
	SourceForwardAuth Source = "forward_auth"
	SourceAPIKey      Source = "api_key"
)

// Principal is an authenticated caller on either plane.
type Principal struct {
	// User is the forward-auth user, or the key name for API-key callers.
	User string
	// Admin reports full administrative privilege.
	Admin bool
	// Grants are the per-group capability grants of an API-key principal.
	Grants []store.Grant
	// Source records how the principal authenticated.
	Source Source
	// KeyID is the api_keys row id for API-key principals.
	KeyID int64
}

// Can reports whether the principal holds capability c on the site identified
// by (group, slug). Admin principals are unconditionally allowed. For API-key
// principals, any grant that includes c and whose subtree covers the site
// suffices.
func (p *Principal) Can(c keys.Capability, group, slug string) bool {
	if p.Admin {
		return true
	}
	if p.Source != SourceAPIKey {
		return false
	}
	for _, g := range p.Grants {
		if keys.HasCapability(g.Permissions, c) && grantCovers(g, group, slug) {
			return true
		}
	}
	return false
}

// CanPublishTo reports whether the principal may publish the given site.
func (p *Principal) CanPublishTo(group, slug string) bool {
	return p.Can(keys.CapPublish, group, slug)
}

// grantCovers reports whether g covers the site identified by (group, slug).
//
//   - A site grant matches exactly one site: its target equals the site's dir.
//   - A group grant matches the whole subtree: any site whose group lies within
//     the target, plus the group's own subdomain (the site whose dir equals the
//     target). An empty target matches everything (global).
func grantCovers(g store.Grant, group, slug string) bool {
	dir := siteDir(group, slug)
	if g.Kind == store.GrantSite {
		return dir == g.Target
	}
	if groupAllowed(g.Target, group) {
		return true
	}
	return g.Target != "" && dir == g.Target
}

// siteDir is the slash-joined directory of a site, e.g. "grp/app" or "app".
func siteDir(group, slug string) string {
	if group == "" {
		return slug
	}
	return group + "/" + slug
}

// groupAllowed reports whether target is within the restriction subtree.
// An empty restriction allows any group.
func groupAllowed(restriction, target string) bool {
	if restriction == "" {
		return true
	}
	return target == restriction || strings.HasPrefix(target, restriction+"/")
}

// Authenticator resolves principals from requests.
type Authenticator struct {
	cfg   *config.Config
	store *store.Store
}

// New returns an Authenticator.
func New(cfg *config.Config, st *store.Store) *Authenticator {
	return &Authenticator{cfg: cfg, store: st}
}

// ForwardAuth resolves the management-plane principal from r, or nil if the
// request carries no trustworthy identity. The identity header is honored only
// when r's direct peer IP is a trusted proxy.
func (a *Authenticator) ForwardAuth(r *http.Request) *Principal {
	peer, ok := peerAddr(r)
	if !ok || !a.cfg.TrustsAddr(peer) {
		return nil
	}
	user := strings.TrimSpace(r.Header.Get(a.cfg.ForwardAuthHeader))
	if user == "" {
		return nil
	}
	return &Principal{
		User:   user,
		Admin:  a.cfg.IsAdminUser(user),
		Source: SourceForwardAuth,
	}
}

// APIKey resolves the ingest-plane principal from the Authorization: Bearer
// token in r, or nil if absent/invalid.
func (a *Authenticator) APIKey(ctx context.Context, r *http.Request) *Principal {
	token := bearerToken(r)
	if token == "" {
		return nil
	}
	rec, err := a.store.FindKeyByHash(ctx, keys.Hash(token))
	if err != nil {
		return nil
	}
	if rec.Expired(time.Now()) {
		return nil
	}
	a.store.TouchKey(ctx, rec.ID)
	return &Principal{
		User:   rec.Name,
		Admin:  rec.Admin,
		Grants: rec.Grants,
		Source: SourceAPIKey,
		KeyID:  rec.ID,
	}
}

// bearerToken extracts a Bearer token from the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// peerAddr returns the direct TCP peer address of the request. This is the
// proxy's address, never a client-asserted forwarded address.
func peerAddr(r *http.Request) (netip.Addr, bool) {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		// Handle IPv6 [::1]:port and IPv4 host:port.
		if h, _, ok := splitHostPort(host); ok {
			host = h
		}
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func splitHostPort(hp string) (host, port string, ok bool) {
	i := strings.LastIndexByte(hp, ':')
	if i < 0 {
		return hp, "", false
	}
	// IPv6 without brackets but with port is ambiguous; require brackets.
	if strings.Count(hp, ":") > 1 && !strings.Contains(hp, "]") {
		return hp, "", false
	}
	return hp[:i], hp[i+1:], true
}
