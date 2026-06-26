package ingest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lmgarret/gotifacts/internal/router"
)

// CurrentRevision is the synthetic revision ID for the live (current) content.
const CurrentRevision = "current"

// ErrRevisionNotFound is returned when a revision ID does not name the current
// content or any retained archived version.
var ErrRevisionNotFound = fmt.Errorf("revision not found")

// Revision describes a browsable version of a site: either the live content
// (Current) or a retained archived snapshot identified by its timestamp.
type Revision struct {
	// ID is "current" for the live content, otherwise the archive timestamp.
	ID string `json:"id"`
	// Current is true for the live content.
	Current bool `json:"current"`
	// CreatedAt is the archive time (parsed from the timestamp) or, for the
	// current revision, the live directory's modification time.
	CreatedAt time.Time `json:"created_at"`
	// Size is the total on-disk size in bytes of the revision's regular files.
	Size int64 `json:"size"`
}

// ListRevisions returns the site's revisions: the current (live) content first,
// followed by archived versions newest-first. A site with no live directory and
// no archives yields an empty slice.
func (p *Publisher) ListRevisions(sp router.SitePath) ([]Revision, error) {
	var revs []Revision

	live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
	if fi, err := os.Stat(live); err == nil && fi.IsDir() {
		size, _ := dirSize(live)
		revs = append(revs, Revision{ID: CurrentRevision, Current: true, CreatedAt: fi.ModTime().UTC(), Size: size})
	}

	verRoot := filepath.Join(p.cfg.VersionsDir(), filepath.FromSlash(sp.Dir()))
	versions, err := listVersions(verRoot)
	if err != nil {
		return nil, err
	}
	// listVersions is ascending (oldest first); emit newest first.
	for i := len(versions) - 1; i >= 0; i-- {
		name := versions[i]
		ts, _ := time.Parse(versionStampLayout, name)
		size, _ := dirSize(filepath.Join(verRoot, name))
		revs = append(revs, Revision{ID: name, CreatedAt: ts.UTC(), Size: size})
	}
	return revs, nil
}

// dirSize returns the total size in bytes of all regular files under dir.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// RevisionDir resolves a revision ID to its on-disk directory. The current
// revision maps to the live directory; any other ID must name a retained
// archived version (this validation also prevents path traversal via the ID).
func (p *Publisher) RevisionDir(sp router.SitePath, rev string) (string, error) {
	if rev == "" || rev == CurrentRevision {
		live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
		if fi, err := os.Stat(live); err != nil || !fi.IsDir() {
			return "", ErrRevisionNotFound
		}
		return live, nil
	}
	verRoot := filepath.Join(p.cfg.VersionsDir(), filepath.FromSlash(sp.Dir()))
	versions, err := listVersions(verRoot)
	if err != nil {
		return "", err
	}
	for _, v := range versions {
		if v == rev {
			return filepath.Join(verRoot, rev), nil
		}
	}
	return "", ErrRevisionNotFound
}

// RollbackTo promotes the named revision to live. The current live content is
// archived first (so the action is reversible), then the chosen revision is
// copied — not moved — into place, leaving it in the version history as well.
// Rolling back to the current revision is a no-op.
func (p *Publisher) RollbackTo(ctx context.Context, sp router.SitePath, rev string) error {
	if !p.cfg.VersioningEnabled {
		return fmt.Errorf("versioning is not enabled")
	}
	if rev == "" || rev == CurrentRevision {
		return nil
	}
	src, err := p.RevisionDir(sp, rev)
	if err != nil {
		return err
	}

	// Copy the chosen revision into a fresh staging dir first, so it survives
	// the prune that archiving the current live may trigger.
	stage, err := p.newStageDir()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := copyDir(src, stage); err != nil {
		return err
	}

	live := filepath.Join(p.cfg.SitesDir(), filepath.FromSlash(sp.Dir()))
	if err := p.swap(stage, live, sp); err != nil {
		return err
	}
	if _, err := p.store.UpsertSiteTouch(ctx, sp.Group, sp.Slug); err != nil {
		return err
	}
	size, _ := dirSize(live)
	return p.store.SetSiteSize(ctx, sp.Group, sp.Slug, size)
}

// copyDir recursively copies the contents of src into dst (which must already
// exist). Only directories and regular files are copied.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
