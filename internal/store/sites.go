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
	ID          int64     `json:"id"`
	Group       string    `json:"group"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Date        string    `json:"date,omitempty"`
	Tags        []string  `json:"tags"`
	Repo        string    `json:"repo,omitempty"`
	Preview     string    `json:"preview,omitempty"`
	Hidden      bool      `json:"hidden"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
        INSERT INTO sites (group_path, slug, title, description, date, tags, repo, preview, hidden, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(group_path, slug) DO UPDATE SET
            title=excluded.title, description=excluded.description, date=excluded.date,
            tags=excluded.tags, repo=excluded.repo, preview=excluded.preview,
            hidden=excluded.hidden, updated_at=excluded.updated_at`,
		group, slug, in.Title, in.Description, in.Date, string(tags), in.Repo, in.Preview, boolInt(in.Hidden), ts, ts)
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
func (s *Store) UpsertSiteTouch(ctx context.Context, group, slug string) (*Site, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE sites SET updated_at=? WHERE group_path=? AND slug=?`, now(), group, slug); err != nil {
		return nil, err
	}
	return s.GetSite(ctx, group, slug)
}

// GetSite returns the site at (group, slug) or ErrNotFound.
func (s *Store) GetSite(ctx context.Context, group, slug string) (*Site, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, group_path, slug, title, description, date, tags, repo, preview, hidden, created_at, updated_at
        FROM sites WHERE group_path=? AND slug=?`, group, slug)
	return scanSite(row)
}

// DeleteSite removes the site at (group, slug). Returns ErrNotFound if absent.
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
	q := `SELECT id, group_path, slug, title, description, date, tags, repo, preview, hidden, created_at, updated_at FROM sites`
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
		s        Site
		tagsJSON string
		hidden   int
		created  string
		updated  string
	)
	err := row.Scan(&s.ID, &s.Group, &s.Slug, &s.Title, &s.Description, &s.Date,
		&tagsJSON, &s.Repo, &s.Preview, &hidden, &created, &updated)
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
