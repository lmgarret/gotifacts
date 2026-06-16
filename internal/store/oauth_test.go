package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lmgarret/gotifacts/internal/keys"
)

func pubGrant(target string) []Grant {
	return []Grant{{Kind: GrantGroup, Target: target, Permissions: []keys.Capability{keys.CapPublish}}}
}

func TestOAuthClientRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if _, err := st.GetOAuthClient(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing client: want ErrNotFound, got %v", err)
	}

	in := OAuthClient{
		ClientID:        "mcp-abc",
		Name:            "Claude",
		RedirectURIs:    []string{"https://claude.ai/cb", "https://claude.com/cb"},
		TokenAuthMethod: "none",
	}
	if err := st.CreateOAuthClient(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetOAuthClient(ctx, "mcp-abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Claude" || len(got.RedirectURIs) != 2 || got.SecretHash != "" {
		t.Fatalf("unexpected client: %+v", got)
	}
}

func TestAuthCodeSingleUseAndExpiry(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	code := AuthCode{
		Hash: "h1", ClientID: "c", User: "alice", RedirectURI: "https://x/cb",
		CodeChallenge: "ch", CodeChallengeMethod: "S256", Grants: pubGrant("claude"),
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := st.CreateAuthCode(ctx, code); err != nil {
		t.Fatal(err)
	}
	got, err := st.ConsumeAuthCode(ctx, "h1")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.User != "alice" || len(got.Grants) != 1 || got.Grants[0].Target != "claude" {
		t.Fatalf("unexpected code: %+v", got)
	}
	if !keys.HasCapability(got.Grants[0].Permissions, keys.CapPublish) {
		t.Fatalf("grant lost publish capability: %+v", got.Grants[0])
	}
	// Second consume must fail — codes are single-use.
	if _, err := st.ConsumeAuthCode(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reuse: want ErrNotFound, got %v", err)
	}

	expired := AuthCode{Hash: "h2", ClientID: "c", User: "bob", RedirectURI: "https://x/cb",
		CodeChallenge: "ch", CodeChallengeMethod: "S256", Grants: pubGrant("claude"),
		ExpiresAt: time.Now().Add(-time.Minute)}
	if err := st.CreateAuthCode(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumeAuthCode(ctx, "h2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired: want ErrNotFound, got %v", err)
	}
}

func TestTokenFindExpiryAndDelete(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	good := Token{Hash: "a", ConnID: "conn1", Kind: "access", ClientID: "c", User: "alice",
		Grants: pubGrant("claude"), ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.CreateToken(ctx, good); err != nil {
		t.Fatal(err)
	}
	got, err := st.FindToken(ctx, "access", "a")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.User != "alice" || got.ConnID != "conn1" || len(got.Grants) != 1 {
		t.Fatalf("unexpected token: %+v", got)
	}
	if _, err := st.FindToken(ctx, "refresh", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong kind: want ErrNotFound, got %v", err)
	}

	expired := Token{Hash: "b", ConnID: "conn1", Kind: "access", ClientID: "c", User: "bob",
		Grants: pubGrant("x"), ExpiresAt: time.Now().Add(-time.Hour)}
	if err := st.CreateToken(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindToken(ctx, "access", "b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired: want ErrNotFound, got %v", err)
	}

	if err := st.DeleteToken(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindToken(ctx, "access", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestConnectionsListAndRevoke(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_ = st.CreateOAuthClient(ctx, OAuthClient{ClientID: "cli", Name: "Claude", RedirectURIs: []string{"https://x/cb"}})

	// One connection = an access + refresh token sharing a conn_id.
	for _, kind := range []string{"access", "refresh"} {
		if err := st.CreateToken(ctx, Token{
			Hash: kind + "-tok", ConnID: "conn-A", Kind: kind, ClientID: "cli", User: "alice",
			Grants: pubGrant("claude"), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A second, expired connection must not appear.
	_ = st.CreateToken(ctx, Token{Hash: "old", ConnID: "conn-B", Kind: "refresh", ClientID: "cli",
		User: "bob", Grants: pubGrant("x"), ExpiresAt: time.Now().Add(-time.Hour)})

	conns, err := st.ListConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 1 {
		t.Fatalf("expected 1 active connection, got %d: %+v", len(conns), conns)
	}
	c := conns[0]
	if c.ID != "conn-A" || c.User != "alice" || c.ClientName != "Claude" || len(c.Grants) != 1 {
		t.Fatalf("unexpected connection: %+v", c)
	}

	// Revoking deletes every token under the conn_id.
	if err := st.DeleteConnection(ctx, "conn-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FindToken(ctx, "access", "access-tok"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("access token survived revoke: %v", err)
	}
	if err := st.DeleteConnection(ctx, "conn-A"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking again: want ErrNotFound, got %v", err)
	}
}
