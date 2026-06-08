package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
		CodeChallenge: "ch", CodeChallengeMethod: "S256", Scope: "publish",
		Group: "claude", ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := st.CreateAuthCode(ctx, code); err != nil {
		t.Fatal(err)
	}
	got, err := st.ConsumeAuthCode(ctx, "h1")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.User != "alice" || got.Group != "claude" {
		t.Fatalf("unexpected code: %+v", got)
	}
	// Second consume must fail — codes are single-use.
	if _, err := st.ConsumeAuthCode(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reuse: want ErrNotFound, got %v", err)
	}

	// Expired code is consumed-and-rejected.
	expired := AuthCode{Hash: "h2", ClientID: "c", User: "bob", RedirectURI: "https://x/cb",
		CodeChallenge: "ch", CodeChallengeMethod: "S256", ExpiresAt: time.Now().Add(-time.Minute)}
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

	good := Token{Hash: "a", Kind: "access", ClientID: "c", User: "alice",
		Scope: "publish", Group: "claude", ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.CreateToken(ctx, good); err != nil {
		t.Fatal(err)
	}
	got, err := st.FindToken(ctx, "access", "a")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.User != "alice" || got.Group != "claude" {
		t.Fatalf("unexpected token: %+v", got)
	}
	// Wrong kind must not match.
	if _, err := st.FindToken(ctx, "refresh", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong kind: want ErrNotFound, got %v", err)
	}

	expired := Token{Hash: "b", Kind: "access", ClientID: "c", User: "bob",
		ExpiresAt: time.Now().Add(-time.Hour)}
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
