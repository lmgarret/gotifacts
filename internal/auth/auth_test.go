package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/keys"
	"github.com/lmgarret/gotifacts/internal/store"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("GOTIFACTS_BASE_DOMAIN", "example.com")
	t.Setenv("GOTIFACTS_ADMIN_USERS", "alice")
	t.Setenv("GOTIFACTS_TRUSTED_PROXIES", "10.0.0.0/8,127.0.0.1")
	t.Setenv("GOTIFACTS_FORWARD_AUTH_HEADER", "Remote-User")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("config invalid: %v", errs)
	}
	return cfg
}

func newReq(remoteAddr, user string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/api/me", nil)
	r.RemoteAddr = remoteAddr
	if user != "" {
		r.Header.Set("Remote-User", user)
	}
	return r
}

func TestForwardAuthHonoredFromTrustedProxy(t *testing.T) {
	a := New(testConfig(t), nil)

	p := a.ForwardAuth(newReq("10.1.2.3:5555", "alice"))
	if p == nil {
		t.Fatal("trusted proxy + header should authenticate")
	}
	if !p.Admin {
		t.Fatal("alice should be admin")
	}

	p = a.ForwardAuth(newReq("10.1.2.3:5555", "bob"))
	if p == nil || p.Admin {
		t.Fatalf("bob should be a non-admin viewer, got %+v", p)
	}
}

func TestForwardAuthIgnoredFromUntrustedSource(t *testing.T) {
	a := New(testConfig(t), nil)
	// Header present but source IP is not trusted → must be ignored.
	if p := a.ForwardAuth(newReq("203.0.113.9:443", "alice")); p != nil {
		t.Fatalf("untrusted source must not authenticate, got %+v", p)
	}
}

func TestForwardAuthNoHeader(t *testing.T) {
	a := New(testConfig(t), nil)
	if p := a.ForwardAuth(newReq("10.0.0.1:1", "")); p != nil {
		t.Fatal("missing header must not authenticate")
	}
}

func TestStripUntrustedIdentity(t *testing.T) {
	a := New(testConfig(t), nil)
	var seen string
	h := a.StripUntrustedIdentity(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Remote-User")
	}))

	// Untrusted source: header stripped.
	h.ServeHTTP(httptest.NewRecorder(), newReq("203.0.113.9:443", "attacker"))
	if seen != "" {
		t.Fatalf("untrusted identity header not stripped: %q", seen)
	}
	// Trusted source: header preserved.
	h.ServeHTTP(httptest.NewRecorder(), newReq("10.0.0.5:22", "alice"))
	if seen != "alice" {
		t.Fatalf("trusted identity header dropped: %q", seen)
	}
}

// newAuth opens a fresh store and an Authenticator bound to the test config.
func newAuth(t *testing.T) (*Authenticator, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/k.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(testConfig(t), st), st
}

// mintPrincipal creates a key with the given shape and resolves it back into a
// Principal via the ingest plane, returning nil when the key is rejected.
func mintPrincipal(t *testing.T, a *Authenticator, st *store.Store, admin bool, grants []store.Grant, expiry *time.Time) *Principal {
	t.Helper()
	tok, hash, _ := keys.Generate()
	if _, err := st.CreateKey(context.Background(), "k", admin, grants, expiry, hash); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	return a.APIKey(context.Background(), r)
}

func TestAPIKeyGroupGrant(t *testing.T) {
	a, st := newAuth(t)
	p := mintPrincipal(t, a, st, false, []store.Grant{{
		Kind:        store.GrantGroup,
		Target:      "claude",
		Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish},
	}}, nil)
	if p == nil || p.Admin {
		t.Fatalf("scoped key should authenticate as non-admin: %+v", p)
	}
	// Within the subtree (group, slug identify the site).
	if !p.Can(keys.CapPublish, "claude", "app") || !p.Can(keys.CapUnpublish, "claude/sub", "app") {
		t.Fatal("key should act within its subtree")
	}
	// Outside the subtree.
	if p.Can(keys.CapPublish, "other", "app") || p.Can(keys.CapPublish, "claudex", "app") {
		t.Fatal("key must be confined to its subtree")
	}
	// Ungranted capabilities are denied even within the subtree.
	if p.Can(keys.CapRollback, "claude", "app") || p.Can(keys.CapPatch, "claude", "app") {
		t.Fatal("key must not hold ungranted capabilities")
	}
	// The grant on "claude" also covers the group's own subdomain (group "",
	// slug "claude"), but no other apex site.
	if !p.Can(keys.CapPublish, "", "claude") {
		t.Fatal("grant on 'claude' should cover the claude.<base> apex site")
	}
	if p.Can(keys.CapPublish, "", "other") {
		t.Fatal("grant on 'claude' must not cover unrelated apex sites")
	}
}

func TestAPIKeySiteGrant(t *testing.T) {
	a, st := newAuth(t)
	p := mintPrincipal(t, a, st, false, []store.Grant{{
		Kind:        store.GrantSite,
		Target:      "docs/app",
		Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish},
	}}, nil)
	if !p.Can(keys.CapPublish, "docs", "app") {
		t.Fatal("site grant should cover its exact site")
	}
	if p.Can(keys.CapPublish, "docs/app", "child") {
		t.Fatal("site grant must not cover children")
	}
	if p.Can(keys.CapPublish, "docs", "other") {
		t.Fatal("site grant must not cover siblings")
	}
}

func TestAPIKeyInvalidToken(t *testing.T) {
	a, _ := newAuth(t)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	r.Header.Set("Authorization", "Bearer gtf_nope")
	if a.APIKey(context.Background(), r) != nil {
		t.Fatal("bogus token must not authenticate")
	}
}

func TestAPIKeyExpiry(t *testing.T) {
	a, st := newAuth(t)
	grant := []store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}}

	past := time.Now().Add(-time.Hour)
	if mintPrincipal(t, a, st, false, grant, &past) != nil {
		t.Fatal("expired key must not authenticate")
	}
	future := time.Now().Add(time.Hour)
	if mintPrincipal(t, a, st, false, grant, &future) == nil {
		t.Fatal("unexpired key should authenticate")
	}
}

func TestAPIKeyAdminScope(t *testing.T) {
	a, st := newAuth(t)
	pa := mintPrincipal(t, a, st, true, nil, nil)
	if pa == nil || !pa.Admin {
		t.Fatalf("admin key should authenticate as admin: %+v", pa)
	}
	for _, c := range []keys.Capability{keys.CapPublish, keys.CapUnpublish, keys.CapRollback, keys.CapPatch} {
		if !pa.Can(c, "anywhere", "x") {
			t.Fatalf("admin key should hold %s anywhere", c)
		}
	}
}
