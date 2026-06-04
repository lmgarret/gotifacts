package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
	r := httptest.NewRequest(http.MethodGet, "http://example.com/api/me", nil)
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

func TestAPIKeyAuthAndScope(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(context.Background(), t.TempDir()+"/k.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	a := New(cfg, st)

	tok, hash, _ := keys.Generate()
	if _, err := st.CreateKey(context.Background(), "ci", keys.ScopePublish, "claude", hash); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "http://example.com/ingest/sites", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	p := a.APIKey(context.Background(), r)
	if p == nil {
		t.Fatal("valid key should authenticate")
	}
	if p.Admin {
		t.Fatal("publish key must not be admin")
	}
	// Scope enforcement: restricted to "claude" subtree.
	if !p.CanPublishTo("claude") || !p.CanPublishTo("claude/sub") {
		t.Fatal("publish key should allow its group subtree")
	}
	if p.CanPublishTo("other") || p.CanPublishTo("claudex") {
		t.Fatal("publish key must be confined to its group subtree")
	}

	// Bogus token rejected.
	r2 := httptest.NewRequest(http.MethodPost, "http://example.com/ingest/sites", nil)
	r2.Header.Set("Authorization", "Bearer gtf_nope")
	if a.APIKey(context.Background(), r2) != nil {
		t.Fatal("bogus token must not authenticate")
	}

	// Forward-auth header must be ignored on the ingest plane (we never call
	// ForwardAuth there); confirm an admin key grants publish anywhere.
	tokA, hashA, _ := keys.Generate()
	_, _ = st.CreateKey(context.Background(), "admin", keys.ScopeAdmin, "", hashA)
	rA := httptest.NewRequest(http.MethodPost, "http://example.com/ingest/sites", nil)
	rA.Header.Set("Authorization", "Bearer "+tokA)
	pa := a.APIKey(context.Background(), rA)
	if pa == nil || !pa.Admin || !pa.CanPublishTo("anywhere") {
		t.Fatalf("admin key should publish anywhere: %+v", pa)
	}
}
