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

// AuthCode is a single-use PKCE authorization code (stored by hash). Grants are
// the capabilities the approving user consented to.
type AuthCode struct {
	Hash                string
	ClientID            string
	User                string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Grants              []Grant
	ExpiresAt           time.Time
}

// Token is an issued access or refresh token (stored by hash). ConnID groups
// the tokens of one connection so they can be revoked together.
type Token struct {
	Hash       string
	ConnID     string
	Kind       string // "access" | "refresh"
	ClientID   string
	User       string
	Grants     []Grant
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Connection is an aggregate view of one MCP connection (all tokens sharing a
// conn_id), surfaced for listing and revocation in the management plane.
type Connection struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	User       string     `json:"user"`
	Grants     []Grant    `json:"grants"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
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
	grants, err := json.Marshal(c.Grants)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO oauth_codes (code_hash, client_id, grant_user, redirect_uri, code_challenge, code_challenge_method, grants, expires_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Hash, c.ClientID, c.User, c.RedirectURI, c.CodeChallenge, c.CodeChallengeMethod,
		string(grants), c.ExpiresAt.UTC().Format(time.RFC3339Nano), now())
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
        SELECT client_id, grant_user, redirect_uri, code_challenge, code_challenge_method, grants, expires_at
        FROM oauth_codes WHERE code_hash=?`, hash)
	var (
		c       AuthCode
		grants  string
		expires string
	)
	c.Hash = hash
	err = row.Scan(&c.ClientID, &c.User, &c.RedirectURI, &c.CodeChallenge, &c.CodeChallengeMethod, &grants, &expires)
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
	c.Grants = decodeGrants(grants)
	c.ExpiresAt = parseTime(expires)
	if c.ExpiresAt.Before(time.Now()) {
		return nil, ErrNotFound
	}
	return &c, nil
}

// CreateToken stores an issued token.
func (s *Store) CreateToken(ctx context.Context, t Token) error {
	grants, err := json.Marshal(t.Grants)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO oauth_tokens (token_hash, conn_id, kind, client_id, grant_user, grants, expires_at, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Hash, t.ConnID, t.Kind, t.ClientID, t.User, string(grants),
		t.ExpiresAt.UTC().Format(time.RFC3339Nano), now())
	return err
}

// FindToken returns a non-expired token of the given kind, or ErrNotFound.
func (s *Store) FindToken(ctx context.Context, kind, hash string) (*Token, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT token_hash, conn_id, kind, client_id, grant_user, grants, expires_at, created_at, last_used_at
        FROM oauth_tokens WHERE token_hash=? AND kind=?`, hash, kind)
	var (
		t       Token
		grants  string
		expires string
		created string
		last    sql.NullString
	)
	err := row.Scan(&t.Hash, &t.ConnID, &t.Kind, &t.ClientID, &t.User, &grants, &expires, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Grants = decodeGrants(grants)
	t.ExpiresAt = parseTime(expires)
	t.CreatedAt = parseTime(created)
	if last.Valid {
		lt := parseTime(last.String)
		t.LastUsedAt = &lt
	}
	if t.ExpiresAt.Before(time.Now()) {
		return nil, ErrNotFound
	}
	return &t, nil
}

// TouchToken records the last-used timestamp for a token (best effort).
func (s *Store) TouchToken(ctx context.Context, hash string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE oauth_tokens SET last_used_at=? WHERE token_hash=?`, now(), hash)
}

// DeleteToken removes a token by hash (used to rotate refresh tokens).
func (s *Store) DeleteToken(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE token_hash=?`, hash)
	return err
}

// ListConnections returns one row per active connection (conn_id), aggregating
// its tokens and joining the client name. Expired tokens are ignored.
func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT t.conn_id, t.client_id, COALESCE(c.client_name, ''), t.grant_user,
               MIN(t.grants), MIN(t.created_at), MAX(t.last_used_at), MAX(t.expires_at)
        FROM oauth_tokens t
        LEFT JOIN oauth_clients c ON c.client_id = t.client_id
        WHERE t.expires_at > ?
        GROUP BY t.conn_id
        ORDER BY MIN(t.created_at) ASC`, now())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Connection
	for rows.Next() {
		var (
			c       Connection
			grants  string
			created string
			last    sql.NullString
			expires string
		)
		if err := rows.Scan(&c.ID, &c.ClientID, &c.ClientName, &c.User, &grants, &created, &last, &expires); err != nil {
			return nil, err
		}
		c.Grants = decodeGrants(grants)
		c.CreatedAt = parseTime(created)
		if last.Valid {
			lt := parseTime(last.String)
			c.LastUsedAt = &lt
		}
		c.ExpiresAt = parseTime(expires)
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteConnection revokes a connection by deleting all of its tokens. Returns
// ErrNotFound if no tokens carried the conn_id.
func (s *Store) DeleteConnection(ctx context.Context, connID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE conn_id=?`, connID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// decodeGrants parses a stored grants JSON array, tolerating empties.
func decodeGrants(s string) []Grant {
	if s == "" {
		return nil
	}
	var g []Grant
	if err := json.Unmarshal([]byte(s), &g); err != nil {
		return nil
	}
	return g
}
