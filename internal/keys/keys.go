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

// Scope enumerates the authorization scopes a key may hold.
type Scope string

// Recognized scopes.
const (
	// ScopeAdmin grants full access: manage keys, delete/patch/rollback, publish anywhere.
	ScopeAdmin Scope = "admin"
	// ScopePublish grants publish-only access, optionally restricted to a group subtree.
	ScopePublish Scope = "publish"
)

// ErrInvalidScope is returned when a scope string is not recognized.
var ErrInvalidScope = errors.New("invalid scope")

// ParseScope validates and normalizes a scope string.
func ParseScope(s string) (Scope, error) {
	switch Scope(strings.ToLower(strings.TrimSpace(s))) {
	case ScopeAdmin:
		return ScopeAdmin, nil
	case ScopePublish:
		return ScopePublish, nil
	default:
		return "", ErrInvalidScope
	}
}

// Valid reports whether s is a recognized scope.
func (s Scope) Valid() bool {
	return s == ScopeAdmin || s == ScopePublish
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
