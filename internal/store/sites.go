package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Site is a registered static site.
type Site struct {
	ID          int64      `json:"id"`
	Group       string     `json:"group"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Date        string     `json:"date,omitempty"`
	Tags        []string   `json:"tags"`
	Repo        string     `json:"repo,omitempty"`
	Preview     string     `json:"preview,omitempty"`
	Hidden      bool       `json:"hidden"`
	Size        int64      `json:"size"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// SiteInput carries the mutable metadata for an upsert or patch.
type SiteInput struct {
	Title       string
	Description string
	Date        string
	Tags        []string
	Repo        string
	Preview     string
	Hidden      bool
	Size        int64
}

// UpsertSite inserts or updates the site at (group, slug), returning the row.
// created_at is preserved on update; updated_at is always refreshed.
func (s *Store) UpsertSite(ctx context.Context, group, slug string, in SiteInput) (*Site, error) {
	tags, err := json.Marshal(normalizeTags(in.Tags))
	if err != nil {
		return nil, err
	}
	ts := now()
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO sites (group_path, slug, title, description, date, tags, repo, preview, hidden, size, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(group_path, slug) DO UPDATE SET
            title=excluded.title, description=excluded.description, date=excluded.date,
            tags=excluded.tags, repo=excluded.repo, preview=excluded.preview,
            hidden=excluded.hidden, size=excluded.size, updated_at=excluded.updated_at,
            deleted_at=NULL`,
		group, slug, in.Title, in.Description, in.Date, string(tags), in.Repo, in.Preview, boolInt(in.Hidden), in.Size, ts, ts)
	if err != nil {
		return nil, err
	}
	return s.GetSite(ctx, group, slug)
}

// PatchSite updates only metadata for an existing site. Missing fields keep
// their prior values when the corresponding pointer is nil.
func (s *Store) PatchSite(ctx context.Context, group, slug string, p SitePatch) (*Site, error) {
	cur, err := s.GetSite(ctx, group, slug)
	if err != nil {
		return nil, err
	}
	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.Description != nil {
		cur.Description = *p.Description
	}
	if p.Date != nil {
		cur.Date = *p.Date
	}
	if p.Tags != nil {
		cur.Tags = normalizeTags(*p.Tags)
	}
	if p.Repo != nil {
		cur.Repo = *p.Repo
	}
	if p.Preview != nil {
		cur.Preview = *p.Preview
	}
	if p.Hidden != nil {
		cur.Hidden = *p.Hidden
	}
	tags, err := json.Marshal(cur.Tags)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
        UPDATE sites SET title=?, description=?, date=?, tags=?, repo=?, preview=?, hidden=?, updated_at=?
        WHERE group_path=? AND slug=?`,
		cur.Title, cur.Description, cur.Date, string(tags), cur.Repo, cur.Preview, boolInt(cur.Hidden), now(), group, slug); err != nil {
		return nil, err
	}
	return s.GetSite(ctx, group, slug)
}

// SitePatch carries optional metadata updates; nil fields are left unchanged.
type SitePatch struct {
	Title       *string
	Description *string
	Date        *string
	Tags        *[]string
	Repo        *string
	Preview     *string
	Hidden      *bool
}

// UpsertSiteTouch refreshes updated_at for an existing site (used by rollback).
// It also clears deleted_at so that rolling back a soft-deleted site restores it.
func (s *Store) UpsertSiteTouch(ctx context.Context, group, slug string) (*Site, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE sites SET updated_at=?, deleted_at=NULL WHERE group_path=? AND slug=?`, now(), group, slug); err != nil {
		return nil, err
	}
	return s.GetSite(ctx, group, slug)
}

// SetSiteSize updates the stored content size (in bytes) for an existing site.
// Used by the publish/rollback paths and the startup backfill, which compute the
// size from the live directory on disk.
func (s *Store) SetSiteSize(ctx context.Context, group, slug string, size int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sites SET size=? WHERE group_path=? AND slug=?`, size, group, slug)
	return err
}

// GetSite returns the site at (group, slug) or ErrNotFound.
// Soft-deleted sites are not returned; use ListDeletedBefore for purge access.
func (s *Store) GetSite(ctx context.Context, group, slug string) (*Site, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, group_path, slug, title, description, date, tags, repo, preview, hidden, size, created_at, updated_at, deleted_at
        FROM sites WHERE group_path=? AND slug=? AND deleted_at IS NULL`, group, slug)
	return scanSite(row)
}

// SoftDeleteSite marks the site at (group, slug) as deleted by setting
// deleted_at. Returns ErrNotFound if the site does not exist or is already
// soft-deleted.
func (s *Store) SoftDeleteSite(ctx context.Context, group, slug string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sites SET deleted_at=? WHERE group_path=? AND slug=? AND deleted_at IS NULL`,
		now(), group, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSite hard-deletes the site at (group, slug) regardless of soft-delete
// state. Used only by the background purge after the retention TTL has elapsed.
func (s *Store) DeleteSite(ctx context.Context, group, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE group_path=? AND slug=?`, group, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDeletedSites returns all soft-deleted sites ordered by deleted_at desc.
// Used by the admin trash view.
func (s *Store) ListDeletedSites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, group_path, slug, title, description, date, tags, repo, preview, hidden, size, created_at, updated_at, deleted_at
        FROM sites WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *site)
	}
	return out, rows.Err()
}

// RestoreSite clears deleted_at on a soft-deleted site, making it visible again.
// Returns ErrNotFound if the site does not exist or is not currently soft-deleted.
func (s *Store) RestoreSite(ctx context.Context, group, slug string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sites SET deleted_at=NULL, updated_at=? WHERE group_path=? AND slug=? AND deleted_at IS NOT NULL`,
		now(), group, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeDeletedSite hard-deletes a soft-deleted site. Returns ErrNotFound if the
// site does not exist or has not been soft-deleted (preventing accidental purge
// of live sites).
func (s *Store) PurgeDeletedSite(ctx context.Context, group, slug string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sites WHERE group_path=? AND slug=? AND deleted_at IS NOT NULL`,
		group, slug)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDeletedBefore returns all soft-deleted sites whose deleted_at is at or
// before the given cutoff. Used by the background purge job.
func (s *Store) ListDeletedBefore(ctx context.Context, cutoff time.Time) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, group_path, slug, title, description, date, tags, repo, preview, hidden, size, created_at, updated_at, deleted_at
        FROM sites WHERE deleted_at IS NOT NULL AND deleted_at <= ?`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *site)
	}
	return out, rows.Err()
}

// ListFilter parameters narrow and order a site listing.
type ListFilter struct {
	Query       string // substring match on slug/title/description
	Tag         string // exact tag match
	Group       string // exact group prefix (group or group subtree)
	Sort        string // "date" (default, desc), "title", "slug"
	IncludeHide bool   // include hidden sites (admins only)
	Limit       int
	Offset      int
}

// ListSites returns sites matching filter, ordered per Sort.
func (s *Store) ListSites(ctx context.Context, f ListFilter) ([]Site, error) {
	var (
		clauses []string
		args    []any
	)
	clauses = append(clauses, "deleted_at IS NULL")
	if !f.IncludeHide {
		clauses = append(clauses, "hidden = 0")
	}
	if f.Query != "" {
		clauses = append(clauses, "(slug LIKE ? OR title LIKE ? OR description LIKE ?)")
		like := "%" + f.Query + "%"
		args = append(args, like, like, like)
	}
	if f.Group != "" {
		clauses = append(clauses, "(group_path = ? OR group_path LIKE ?)")
		args = append(args, f.Group, f.Group+"/%")
	}
	if f.Tag != "" {
		// Tags are a JSON array; match exact element.
		clauses = append(clauses, `EXISTS (SELECT 1 FROM json_each(sites.tags) WHERE json_each.value = ?)`)
		args = append(args, f.Tag)
	}
	q := `SELECT id, group_path, slug, title, description, date, tags, repo, preview, hidden, size, created_at, updated_at, deleted_at FROM sites`
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	switch f.Sort {
	case "title":
		q += " ORDER BY title COLLATE NOCASE ASC, slug ASC"
	case "slug":
		q += " ORDER BY group_path ASC, slug ASC"
	default:
		q += " ORDER BY date DESC, updated_at DESC"
	}
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *site)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSite(row scanner) (*Site, error) {
	var (
		s         Site
		tagsJSON  string
		hidden    int
		created   string
		updated   string
		deletedAt *string
	)
	err := row.Scan(&s.ID, &s.Group, &s.Slug, &s.Title, &s.Description, &s.Date,
		&tagsJSON, &s.Repo, &s.Preview, &hidden, &s.Size, &created, &updated, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Hidden = hidden != 0
	if err := json.Unmarshal([]byte(tagsJSON), &s.Tags); err != nil || s.Tags == nil {
		s.Tags = []string{}
	}
	s.CreatedAt = parseTime(created)
	s.UpdatedAt = parseTime(updated)
	if deletedAt != nil {
		t := parseTime(*deletedAt)
		s.DeletedAt = &t
	}
	return &s, nil
}

func parseTime(v string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t
	}
	return time.Time{}
}

func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
