// Package keys handles API-key token generation, hashing, and scope semantics.
//
// Tokens have the form "gtf_<base64url-32B>" and are shown in plaintext exactly
// once, at creation. Only the SHA-256 hash is persisted; lookups compare hashes
// in constant time.
package keys

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// TokenPrefix prefixes every generated token.
const TokenPrefix = "gtf_"

// tokenBytes is the number of random bytes in a token.
const tokenBytes = 32

// Capability is a single action a key may be granted on a group subtree.
type Capability string

// Recognized capabilities. A key holds a set of these per grant; an admin key
// implicitly holds all of them everywhere.
const (
	// CapPublish allows creating/updating sites (POST /ingest/sites).
	CapPublish Capability = "publish"
	// CapUnpublish allows deleting sites (DELETE /ingest/sites).
	CapUnpublish Capability = "unpublish"
	// CapRollback allows restoring a site's previous version.
	CapRollback Capability = "rollback"
	// CapPatch allows editing site metadata (PATCH /ingest/sites).
	CapPatch Capability = "patch"
	// CapPurge allows permanently deleting a soft-deleted (quarantined) site.
	CapPurge Capability = "purge"
)

// ErrInvalidCapability is returned when a capability string is not recognized.
var ErrInvalidCapability = errors.New("invalid capability")

// ParseCapability validates and normalizes a capability string.
func ParseCapability(s string) (Capability, error) {
	c := Capability(strings.ToLower(strings.TrimSpace(s)))
	if c.Valid() {
		return c, nil
	}
	return "", ErrInvalidCapability
}

// Valid reports whether c is a recognized capability.
func (c Capability) Valid() bool {
	switch c {
	case CapPublish, CapUnpublish, CapRollback, CapPatch, CapPurge:
		return true
	default:
		return false
	}
}

// ParseCapabilities parses and validates a comma-separated capability list,
// returning a deduplicated set. An empty/whitespace list yields an error.
func ParseCapabilities(csv string) ([]Capability, error) {
	seen := map[Capability]bool{}
	var out []Capability
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		c, err := ParseCapability(part)
		if err != nil {
			return nil, err
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, ErrInvalidCapability
	}
	return out, nil
}

// HasCapability reports whether caps contains target.
func HasCapability(caps []Capability, target Capability) bool {
	for _, c := range caps {
		if c == target {
			return true
		}
	}
	return false
}

// JoinCapabilities renders caps as a comma-separated string for storage.
func JoinCapabilities(caps []Capability) string {
	parts := make([]string, len(caps))
	for i, c := range caps {
		parts[i] = string(c)
	}
	return strings.Join(parts, ",")
}

// Generate returns a new plaintext token and its hex-encoded SHA-256 hash.
func Generate() (token, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return token, Hash(token), nil
}

// Hash returns the hex-encoded SHA-256 of a token.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Equal compares two token hashes in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
