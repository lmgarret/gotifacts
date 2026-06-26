// Package ingest implements site publishing: staging an upload to a temp dir on
// the same volume, validating it, atomically swapping it into place (with
// optional versioning), and recording the registry row in a transaction.
package ingest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lmgarret/gotifacts/internal/archive"
	"github.com/lmgarret/gotifacts/internal/config"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

// Publisher coordinates atomic site publishing.
type Publisher struct {
	cfg   *config.Config
	store *store.Store
}

// New returns a Publisher.
func New(cfg *config.Config, st *store.Store) *Publisher {
	return &Publisher{cfg: cfg, store: st}
}

// Meta is the publish metadata supplied alongside content.
type Meta struct {
	Group       string   `json:"group"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Date        string   `json:"date"`
	Tags        []string `json:"tags"`
	Repo        string   `json:"repo"`
	Preview     string   `json:"preview"`
	Hidden      bool     `json:"hidden"`
}

// Kind distinguishes the content payload form.
type Kind int

// Payload kinds.
const (
	// KindBundle is a gzip-compressed tar containing a top-level index.html.
	KindBundle Kind = iota
	// KindIndex is a single self-contained HTML document.
	KindIndex
	// KindZip is a zip archive containing an index.html (a single common
	// top-level directory is unwrapped during extraction).
	KindZip
)

// Result is returned on a successful publish.
type Result struct {
	URL       string    `json:"url"`
	Group     string    `json:"group"`
	Slug      string    `json:"slug"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Publish stages content from r, validates it, swaps it live, and upserts the
// registry row. group/slug are resolved and validated against the path rules.
func (p *Publisher) Publish(ctx context.Context, meta Meta, kind Kind, r io.Reader) (*Result, *store.Site, error) {
	sp, err := router.NewSitePath(meta.Group, meta.Slug)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid site path: %w", err)
	}

	stage, err := p.newStageDir()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	switch kind {
	case KindBundle:
		if err := archive.ExtractTarGz(r, stage, archive.Limits{
			MaxBytes:   p.cfg.MaxExtractBytes,
			MaxEntries: p.cfg.MaxExtractEntries,
		}); err != nil {
			return nil, nil, err
		}
	case KindZip:
		ra, size, err := readerAtSize(r)
		if err != nil {
			return nil, nil, err
		}
		if err := archive.ExtractZip(ra, size, stage, archive.Limits{
			MaxBytes:   p.cfg.MaxExtractBytes,
			MaxEntries: p.cfg.MaxExtractEntries,
		}); err != nil {
			return nil, nil, err
		}
	case KindIndex:
		if err := archive.WriteSingleIndex(r, stage, p.cfg.MaxExtractBytes); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unknown payload kind")
	}

	if _, err := os.Stat(filepath.Join(stage, "index.html")); err != nil {
		return nil, nil, archive.ErrNoIndex
	}

	live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
	if err := p.swap(stage, live, sp); err != nil {
		return nil, nil, err
	}

	size, _ := dirSize(live)
	site, err := p.store.UpsertSite(ctx, sp.Group, sp.Slug, store.SiteInput{
		Title:       meta.Title,
		Description: meta.Description,
		Date:        normalizeDate(meta.Date),
		Tags:        meta.Tags,
		Repo:        meta.Repo,
		Preview:     meta.Preview,
		Hidden:      meta.Hidden,
		Size:        size,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("registry upsert: %w", err)
	}
	return &Result{
		URL:       sp.URL(p.cfg.BaseDomain),
		Group:     sp.Group,
		Slug:      sp.Slug,
		UpdatedAt: site.UpdatedAt,
	}, site, nil
}

// swap moves stage into place at live, archiving any existing content when
// versioning is enabled.
func (p *Publisher) swap(stage, live string, sp router.SitePath) error {
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(live)
	exists := statErr == nil

	if exists {
		if p.cfg.VersioningEnabled {
			if err := p.archiveVersion(live, sp); err != nil {
				return err
			}
		} else {
			if err := os.RemoveAll(live); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(stage, live); err != nil {
		return fmt.Errorf("swap: %w", err)
	}
	return nil
}

// versionStampLayout is the time format used to name archived version
// directories. It is lexicographically sortable (sort order == time order).
const versionStampLayout = "20060102T150405.000000000Z"

// archiveVersion moves live into the versions dir under a timestamp, then prunes.
func (p *Publisher) archiveVersion(live string, sp router.SitePath) error {
	verRoot := filepath.Join(p.cfg.VersionsDir(), filepath.FromSlash(sp.Dir()))
	if err := os.MkdirAll(verRoot, 0o755); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format(versionStampLayout)
	dst := filepath.Join(verRoot, stamp)
	if err := os.Rename(live, dst); err != nil {
		return fmt.Errorf("archive version: %w", err)
	}
	return p.pruneVersions(verRoot)
}

func (p *Publisher) pruneVersions(verRoot string) error {
	versions, err := listVersions(verRoot)
	if err != nil {
		return err
	}
	keep := p.cfg.VersioningKeep
	if len(versions) <= keep {
		return nil
	}
	for _, v := range versions[:len(versions)-keep] {
		if err := os.RemoveAll(filepath.Join(verRoot, v)); err != nil {
			return err
		}
	}
	return nil
}

// Unpublish soft-deletes a site: it marks the registry row with deleted_at and
// moves the live files to the quarantine directory so the site is immediately
// taken offline. The files remain in quarantine until PurgeDeleted removes them
// after the configured TTL.
func (p *Publisher) Unpublish(ctx context.Context, group, slug string) error {
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		return fmt.Errorf("invalid site path: %w", err)
	}
	if err := p.store.SoftDeleteSite(ctx, group, slug); err != nil {
		return err
	}
	live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
	if _, statErr := os.Stat(live); statErr == nil {
		dst := filepath.Join(p.cfg.DeletedDir(), filepath.FromSlash(sp.Dir()))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("create quarantine dir: %w", err)
		}
		_ = os.RemoveAll(dst) // replace any prior quarantine entry for this slug
		if err := os.Rename(live, dst); err != nil {
			return fmt.Errorf("quarantine site: %w", err)
		}
	}
	return nil
}

// Restore brings a soft-deleted site back online: it clears deleted_at in the
// registry and moves the quarantined files back to the live directory. Returns
// store.ErrNotFound if the site is not currently soft-deleted.
func (p *Publisher) Restore(ctx context.Context, group, slug string) error {
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		return fmt.Errorf("invalid site path: %w", err)
	}
	if err := p.store.RestoreSite(ctx, group, slug); err != nil {
		return err
	}
	quarantine := filepath.Join(p.cfg.DeletedDir(), filepath.FromSlash(sp.Dir()))
	if _, statErr := os.Stat(quarantine); statErr == nil {
		live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
		if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
			return fmt.Errorf("restore: create site dir: %w", err)
		}
		_ = os.RemoveAll(live) // clear any orphaned live dir
		if err := os.Rename(quarantine, live); err != nil {
			return fmt.Errorf("restore: move from quarantine: %w", err)
		}
	}
	return nil
}

// Purge immediately hard-deletes a single soft-deleted (quarantined) site,
// bypassing the TTL. Returns store.ErrNotFound if the site is not currently
// soft-deleted, preventing accidental destruction of live sites.
func (p *Publisher) Purge(ctx context.Context, group, slug string) error {
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		return fmt.Errorf("invalid site path: %w", err)
	}
	if err := p.store.PurgeDeletedSite(ctx, group, slug); err != nil {
		return err
	}
	quarantine := filepath.Join(p.cfg.DeletedDir(), filepath.FromSlash(sp.Dir()))
	_ = os.RemoveAll(quarantine)
	return nil
}

// PurgeDeleted hard-deletes sites whose deleted_at has exceeded the configured
// TTL: it removes the quarantine directory and the database row. Returns the
// number of sites purged.
func (p *Publisher) PurgeDeleted(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-p.cfg.DeletedSiteTTL)
	sites, err := p.store.ListDeletedBefore(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	var n int
	for _, site := range sites {
		sp, err := router.NewSitePath(site.Group, site.Slug)
		if err != nil {
			continue
		}
		dst := filepath.Join(p.cfg.DeletedDir(), filepath.FromSlash(sp.Dir()))
		_ = os.RemoveAll(dst)
		if err := p.store.DeleteSite(ctx, site.Group, site.Slug); err == nil {
			n++
		}
	}
	return n, nil
}

// BackfillSizes recomputes and stores the content size for every live site by
// walking its live directory. It is run once at startup so that sites published
// before the size column existed report an accurate size.
func (p *Publisher) BackfillSizes(ctx context.Context) error {
	sites, err := p.store.ListSites(ctx, store.ListFilter{IncludeHide: true})
	if err != nil {
		return err
	}
	for _, site := range sites {
		sp, err := router.NewSitePath(site.Group, site.Slug)
		if err != nil {
			continue
		}
		live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
		size, err := dirSize(live)
		if err != nil {
			continue
		}
		if size == site.Size {
			continue
		}
		if err := p.store.SetSiteSize(ctx, site.Group, site.Slug, size); err != nil {
			return err
		}
	}
	return nil
}

// Rollback restores the most recent archived version of a site, swapping the
// current live content into the version history.
func (p *Publisher) Rollback(ctx context.Context, sp router.SitePath) error {
	if !p.cfg.VersioningEnabled {
		return fmt.Errorf("versioning is not enabled")
	}
	verRoot := filepath.Join(p.cfg.VersionsDir(), filepath.FromSlash(sp.Dir()))
	versions, err := listVersions(verRoot)
	if err != nil || len(versions) == 0 {
		return fmt.Errorf("no versions to roll back to")
	}
	latest := versions[len(versions)-1]
	src := filepath.Join(verRoot, latest)
	live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))

	// Stage current live into versions (so rollback is itself reversible),
	// then move the chosen version into place.
	if _, statErr := os.Stat(live); statErr == nil {
		if err := p.archiveVersion(live, sp); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, live); err != nil {
		return err
	}
	if _, err := p.store.UpsertSiteTouch(ctx, sp.Group, sp.Slug); err != nil {
		return err
	}
	size, _ := dirSize(live)
	return p.store.SetSiteSize(ctx, sp.Group, sp.Slug, size)
}

// readerAtSize adapts a content stream into the io.ReaderAt + size that
// archive/zip needs. The publish path spools to a temp file, so the common
// cases (*os.File, *bytes.Reader) are zero-copy; anything else is buffered.
func readerAtSize(r io.Reader) (io.ReaderAt, int64, error) {
	switch v := r.(type) {
	case *os.File:
		st, err := v.Stat()
		if err != nil {
			return nil, 0, err
		}
		return v, st.Size(), nil
	case *bytes.Reader:
		return v, v.Size(), nil
	default:
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, 0, err
		}
		return bytes.NewReader(data), int64(len(data)), nil
	}
}

func (p *Publisher) newStageDir() (string, error) {
	if err := os.MkdirAll(p.cfg.TmpDir(), 0o755); err != nil {
		return "", err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(p.cfg.TmpDir(), hex.EncodeToString(b[:]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// listVersions returns version directory names sorted ascending (oldest first).
func listVersions(verRoot string) ([]string, error) {
	entries, err := os.ReadDir(verRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func normalizeDate(d string) string {
	if d == "" {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return d
}
