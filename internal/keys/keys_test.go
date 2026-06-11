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

func TestParseCapability(t *testing.T) {
	if c, err := ParseCapability("PUBLISH"); err != nil || c != CapPublish {
		t.Fatalf("publish: %v %v", c, err)
	}
	if c, err := ParseCapability(" unpublish "); err != nil || c != CapUnpublish {
		t.Fatalf("unpublish: %v %v", c, err)
	}
	if _, err := ParseCapability("root"); err == nil {
		t.Fatal("expected invalid capability error")
	}
}

func TestParseCapabilities(t *testing.T) {
	caps, err := ParseCapabilities("publish, unpublish ,publish")
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 2 || !HasCapability(caps, CapPublish) || !HasCapability(caps, CapUnpublish) {
		t.Fatalf("unexpected dedup result: %v", caps)
	}
	if JoinCapabilities(caps) != "publish,unpublish" {
		t.Fatalf("unexpected join: %q", JoinCapabilities(caps))
	}
	if _, err := ParseCapabilities("  ,  "); err == nil {
		t.Fatal("expected error for empty capability list")
	}
	if _, err := ParseCapabilities("publish,bogus"); err == nil {
		t.Fatal("expected error for invalid capability")
	}
}
