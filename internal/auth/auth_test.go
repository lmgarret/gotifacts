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

func TestAPIKeyAuthAndCapabilities(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(context.Background(), t.TempDir()+"/k.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	a := New(cfg, st)

	// A CI key that may publish and unpublish within the "claude" subtree.
	tok, hash, _ := keys.Generate()
	grants := []store.Grant{{
		Kind:        store.GrantGroup,
		Target:      "claude",
		Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish},
	}}
	if _, err := st.CreateKey(context.Background(), "ci", false, grants, nil, hash); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	p := a.APIKey(context.Background(), r)
	if p == nil {
		t.Fatal("valid key should authenticate")
	}
	if p.Admin {
		t.Fatal("scoped key must not be admin")
	}
	// Capability + subtree enforcement. (group, slug) identifies the site.
	if !p.Can(keys.CapPublish, "claude", "app") || !p.Can(keys.CapPublish, "claude/sub", "app") {
		t.Fatal("key should publish within its subtree")
	}
	if !p.Can(keys.CapUnpublish, "claude/sub", "app") {
		t.Fatal("key should unpublish within its subtree")
	}
	if p.Can(keys.CapPublish, "other", "app") || p.Can(keys.CapPublish, "claudex", "app") {
		t.Fatal("key must be confined to its subtree")
	}
	// Ungranted capabilities are denied even within the subtree.
	if p.Can(keys.CapRollback, "claude", "app") || p.Can(keys.CapPatch, "claude", "app") {
		t.Fatal("key must not hold ungranted capabilities")
	}
	// The grant on "claude" also covers the group's own subdomain — the flat
	// site at claude.<base> (group "", slug "claude") — but no other apex site.
	if !p.Can(keys.CapPublish, "", "claude") {
		t.Fatal("grant on 'claude' should cover the claude.<base> apex site")
	}
	if p.Can(keys.CapPublish, "", "other") || p.Can(keys.CapPublish, "", "claudex") {
		t.Fatal("grant on 'claude' must not cover unrelated apex sites")
	}

	// A site grant covers exactly one site — neither children nor siblings.
	tokS, hashS, _ := keys.Generate()
	_, _ = st.CreateKey(context.Background(), "site", false, []store.Grant{{
		Kind:        store.GrantSite,
		Target:      "docs/app",
		Permissions: []keys.Capability{keys.CapPublish, keys.CapUnpublish},
	}}, nil, hashS)
	rS := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	rS.Header.Set("Authorization", "Bearer "+tokS)
	ps := a.APIKey(context.Background(), rS)
	if !ps.Can(keys.CapPublish, "docs", "app") {
		t.Fatal("site grant should cover its exact site")
	}
	if ps.Can(keys.CapPublish, "docs/app", "child") || ps.Can(keys.CapPublish, "docs", "other") {
		t.Fatal("site grant must not cover children or siblings")
	}

	// Bogus token rejected.
	r2 := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	r2.Header.Set("Authorization", "Bearer gtf_nope")
	if a.APIKey(context.Background(), r2) != nil {
		t.Fatal("bogus token must not authenticate")
	}

	// An expired key is rejected even though it is otherwise valid.
	tokE, hashE, _ := keys.Generate()
	past := time.Now().Add(-time.Hour)
	_, _ = st.CreateKey(context.Background(), "expired", false,
		[]store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}},
		&past, hashE)
	rE := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	rE.Header.Set("Authorization", "Bearer "+tokE)
	if a.APIKey(context.Background(), rE) != nil {
		t.Fatal("expired key must not authenticate")
	}
	// A future expiry still authenticates.
	tokF, hashF, _ := keys.Generate()
	future := time.Now().Add(time.Hour)
	_, _ = st.CreateKey(context.Background(), "future", false,
		[]store.Grant{{Kind: store.GrantGroup, Target: "claude", Permissions: []keys.Capability{keys.CapPublish}}},
		&future, hashF)
	rF := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	rF.Header.Set("Authorization", "Bearer "+tokF)
	if a.APIKey(context.Background(), rF) == nil {
		t.Fatal("unexpired key should authenticate")
	}

	// An admin key holds every capability everywhere.
	tokA, hashA, _ := keys.Generate()
	_, _ = st.CreateKey(context.Background(), "admin", true, nil, nil, hashA)
	rA := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.com/ingest/sites", nil)
	rA.Header.Set("Authorization", "Bearer "+tokA)
	pa := a.APIKey(context.Background(), rA)
	if pa == nil || !pa.Admin {
		t.Fatalf("admin key should authenticate as admin: %+v", pa)
	}
	for _, c := range []keys.Capability{keys.CapPublish, keys.CapUnpublish, keys.CapRollback, keys.CapPatch} {
		if !pa.Can(c, "anywhere", "x") {
			t.Fatalf("admin key should hold %s anywhere", c)
		}
	}
}
