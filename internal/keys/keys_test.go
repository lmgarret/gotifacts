package keys

import (
	"strings"
	"testing"
)

func TestGenerateAndHash(t *testing.T) {
	tok, hash, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, TokenPrefix) {
		t.Fatalf("token missing prefix: %q", tok)
	}
	if len(hash) != 64 { // hex SHA-256
		t.Fatalf("unexpected hash length %d", len(hash))
	}
	if Hash(tok) != hash {
		t.Fatal("Hash not deterministic for the same token")
	}
	// Distinct tokens.
	tok2, _, _ := Generate()
	if tok == tok2 {
		t.Fatal("tokens are not unique")
	}
}

func TestEqualConstantTime(t *testing.T) {
	_, h, _ := Generate()
	if !Equal(h, h) {
		t.Fatal("equal hashes compared unequal")
	}
	if Equal(h, h[:len(h)-1]+"0") {
		t.Fatal("different hashes compared equal")
	}
}

func TestParseScope(t *testing.T) {
	if s, err := ParseScope("ADMIN"); err != nil || s != ScopeAdmin {
		t.Fatalf("admin: %v %v", s, err)
	}
	if s, err := ParseScope(" publish "); err != nil || s != ScopePublish {
		t.Fatalf("publish: %v %v", s, err)
	}
	if _, err := ParseScope("root"); err == nil {
		t.Fatal("expected invalid scope error")
	}
}
