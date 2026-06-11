package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lmgarret/gotifacts/internal/keys"
)

// Grant pairs a group subtree with the capabilities a key holds within it. An
// empty Group means "any group" (only valid for non-destructive capabilities).
type Grant struct {
	Group       string            `json:"group"`
	Permissions []keys.Capability `json:"permissions"`
}

// APIKey is a stored API key record. The plaintext token is never persisted.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Admin      bool       `json:"admin"`
	Grants     []Grant    `json:"grants"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// CreateKey inserts a key record (and its grants) given its precomputed hash.
// Admin keys carry no grants; their privilege is unconditional.
func (s *Store) CreateKey(ctx context.Context, name string, admin bool, grants []Grant, hash string) (*APIKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	adminInt := 0
	if admin {
		adminInt = 1
	}
	res, err := tx.ExecContext(ctx, `
        INSERT INTO api_keys (name, key_hash, admin, created_at)
        VALUES (?, ?, ?, ?)`, name, hash, adminInt, now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if !admin {
		for _, g := range grants {
			if _, err := tx.ExecContext(ctx, `
                INSERT INTO api_key_grants (key_id, group_path, permissions)
                VALUES (?, ?, ?)`, id, g.Group, keys.JoinCapabilities(g.Permissions)); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKey(ctx, id)
}

// GetKey returns the key with the given id or ErrNotFound.
func (s *Store) GetKey(ctx context.Context, id int64) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, name, admin, created_at, last_used_at
        FROM api_keys WHERE id=?`, id)
	k, err := scanKey(row)
	if err != nil {
		return nil, err
	}
	if err := s.loadGrants(ctx, k); err != nil {
		return nil, err
	}
	return k, nil
}

// FindKeyByHash looks up a key by its hash (constant-time comparison happens at
// the DB level via the unique index; the hash itself is not secret-derived on a
// per-request basis). Returns ErrNotFound if absent.
func (s *Store) FindKeyByHash(ctx context.Context, hash string) (*APIKey, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, name, admin, created_at, last_used_at
        FROM api_keys WHERE key_hash=?`, hash)
	k, err := scanKey(row)
	if err != nil {
		return nil, err
	}
	if err := s.loadGrants(ctx, k); err != nil {
		return nil, err
	}
	return k, nil
}

// loadGrants populates k.Grants from the api_key_grants table.
func (s *Store) loadGrants(ctx context.Context, k *APIKey) error {
	rows, err := s.db.QueryContext(ctx, `
        SELECT group_path, permissions FROM api_key_grants
        WHERE key_id=? ORDER BY id ASC`, k.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var group, perms string
		if err := rows.Scan(&group, &perms); err != nil {
			return err
		}
		caps, _ := keys.ParseCapabilities(perms)
		k.Grants = append(k.Grants, Grant{Group: group, Permissions: caps})
	}
	return rows.Err()
}

// TouchKey records the last-used timestamp for a key (best effort).
func (s *Store) TouchKey(ctx context.Context, id int64) {
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now(), id)
}

// ListKeys returns all key records ordered by creation time.
func (s *Store) ListKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, name, admin, created_at, last_used_at
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadGrants(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
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
		admin   int
		created string
		last    sql.NullString
	)
	err := row.Scan(&k.ID, &k.Name, &admin, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.Admin = admin != 0
	k.CreatedAt = parseTime(created)
	if last.Valid {
		t := parseTime(last.String)
		k.LastUsedAt = &t
	}
	return &k, nil
}
