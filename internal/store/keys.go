package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lmgarret/gotifacts/internal/keys"
)

// APIKey is a stored API key record. The plaintext token is never persisted.
type APIKey struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Scope            keys.Scope `json:"scope"`
	GroupRestriction string     `json:"group_restriction,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
}

// CreateKey inserts a key record given its precomputed hash.
func (s *Store) CreateKey(ctx context.Context, name string, scope keys.Scope, groupRestriction, hash string) (*APIKey, error) {
	var gr any
	if groupRestriction != "" {
		gr = groupRestriction
	}
	res, err := s.db.ExecContext(ctx, `
        INSERT INTO api_keys (name, key_hash, scope, group_restriction, created_at)
        VALUES (?, ?, ?, ?, ?)`, name, hash, string(scope), gr, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetKey(ctx, id)
}

// GetKey returns the key with the given id or ErrNotFound.
func (s *Store) GetKey(ctx context.Context, id int64) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, name, scope, group_restriction, created_at, last_used_at
        FROM api_keys WHERE id=?`, id)
	return scanKey(row)
}

// FindKeyByHash looks up a key by its hash (constant-time comparison happens at
// the DB level via the unique index; the hash itself is not secret-derived on a
// per-request basis). Returns ErrNotFound if absent.
func (s *Store) FindKeyByHash(ctx context.Context, hash string) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, name, scope, group_restriction, created_at, last_used_at
        FROM api_keys WHERE key_hash=?`, hash)
	return scanKey(row)
}

// TouchKey records the last-used timestamp for a key (best effort).
func (s *Store) TouchKey(ctx context.Context, id int64) {
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now(), id)
}

// ListKeys returns all key records ordered by creation time.
func (s *Store) ListKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, name, scope, group_restriction, created_at, last_used_at
        FROM api_keys ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// DeleteKey removes the key with id. Returns ErrNotFound if absent.
func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanKey(row scanner) (*APIKey, error) {
	var (
		k       APIKey
		scope   string
		gr      sql.NullString
		created string
		last    sql.NullString
	)
	err := row.Scan(&k.ID, &k.Name, &scope, &gr, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.Scope = keys.Scope(scope)
	k.GroupRestriction = gr.String
	k.CreatedAt = parseTime(created)
	if last.Valid {
		t := parseTime(last.String)
		k.LastUsedAt = &t
	}
	return &k, nil
}
