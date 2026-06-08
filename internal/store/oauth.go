package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// OAuthClient is a registered MCP OAuth client. SecretHash is empty for public
// (PKCE-only) clients, which is the norm for dynamically-registered connectors.
type OAuthClient struct {
	ClientID        string
	SecretHash      string
	Name            string
	RedirectURIs    []string
	TokenAuthMethod string
	CreatedAt       time.Time
}

// AuthCode is a single-use PKCE authorization code (stored by hash).
type AuthCode struct {
	Hash                string
	ClientID            string
	User                string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	Group               string
	ExpiresAt           time.Time
}

// Token is an issued access or refresh token (stored by hash).
type Token struct {
	Hash      string
	Kind      string // "access" | "refresh"
	ClientID  string
	User      string
	Scope     string
	Group     string
	ExpiresAt time.Time
}

// CreateOAuthClient inserts a registered client.
func (s *Store) CreateOAuthClient(ctx context.Context, c OAuthClient) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}
	var secret any
	if c.SecretHash != "" {
		secret = c.SecretHash
	}
	method := c.TokenAuthMethod
	if method == "" {
		method = "none"
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO oauth_clients (client_id, secret_hash, client_name, redirect_uris, token_auth_method, created_at)
        VALUES (?, ?, ?, ?, ?, ?)`,
		c.ClientID, secret, c.Name, string(uris), method, now())
	return err
}

// GetOAuthClient returns the client with the given id or ErrNotFound.
func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (*OAuthClient, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT client_id, secret_hash, client_name, redirect_uris, token_auth_method, created_at
        FROM oauth_clients WHERE client_id=?`, clientID)
	var (
		c       OAuthClient
		secret  sql.NullString
		uris    string
		created string
	)
	err := row.Scan(&c.ClientID, &secret, &c.Name, &uris, &c.TokenAuthMethod, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.SecretHash = secret.String
	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		c.RedirectURIs = nil
	}
	c.CreatedAt = parseTime(created)
	return &c, nil
}

// CreateAuthCode stores a single-use authorization code.
func (s *Store) CreateAuthCode(ctx context.Context, c AuthCode) error {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO oauth_codes (code_hash, client_id, grant_user, redirect_uri, code_challenge, code_challenge_method, scope, group_path, expires_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Hash, c.ClientID, c.User, c.RedirectURI, c.CodeChallenge, c.CodeChallengeMethod,
		c.Scope, c.Group, c.ExpiresAt.UTC().Format(time.RFC3339Nano), now())
	return err
}

// ConsumeAuthCode atomically fetches and deletes a code by hash, returning
// ErrNotFound if it is absent or expired.
func (s *Store) ConsumeAuthCode(ctx context.Context, hash string) (*AuthCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
        SELECT client_id, grant_user, redirect_uri, code_challenge, code_challenge_method, scope, group_path, expires_at
        FROM oauth_codes WHERE code_hash=?`, hash)
	var (
		c       AuthCode
		expires string
	)
	c.Hash = hash
	err = row.Scan(&c.ClientID, &c.User, &c.RedirectURI, &c.CodeChallenge, &c.CodeChallengeMethod, &c.Scope, &c.Group, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_codes WHERE code_hash=?`, hash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	c.ExpiresAt = parseTime(expires)
	if c.ExpiresAt.Before(time.Now()) {
		return nil, ErrNotFound
	}
	return &c, nil
}

// CreateToken stores an issued token.
func (s *Store) CreateToken(ctx context.Context, t Token) error {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO oauth_tokens (token_hash, kind, client_id, grant_user, scope, group_path, expires_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Hash, t.Kind, t.ClientID, t.User, t.Scope, t.Group, t.ExpiresAt.UTC().Format(time.RFC3339Nano), now())
	return err
}

// FindToken returns a non-expired token of the given kind, or ErrNotFound.
func (s *Store) FindToken(ctx context.Context, kind, hash string) (*Token, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT token_hash, kind, client_id, grant_user, scope, group_path, expires_at
        FROM oauth_tokens WHERE token_hash=? AND kind=?`, hash, kind)
	var (
		t       Token
		expires string
	)
	err := row.Scan(&t.Hash, &t.Kind, &t.ClientID, &t.User, &t.Scope, &t.Group, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.ExpiresAt = parseTime(expires)
	if t.ExpiresAt.Before(time.Now()) {
		return nil, ErrNotFound
	}
	return &t, nil
}

// DeleteToken removes a token by hash (used to rotate refresh tokens).
func (s *Store) DeleteToken(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE token_hash=?`, hash)
	return err
}
