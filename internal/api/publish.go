package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lmgarret/gotifacts/internal/ingest"
)

// parseMultipartPublish parses a publish request: a JSON "meta" part plus either
// a "bundle" (.tar.gz or .zip, distinguished by magic bytes) or an "index"
// (single HTML) part. The returned reader streams the content; cleanup must be
// called when done.
func parseMultipartPublish(w http.ResponseWriter, r *http.Request, maxBytes int64) (ingest.Meta, ingest.Kind, io.Reader, func(), error) {
	noop := func() {}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		return ingest.Meta{}, 0, nil, noop, fmt.Errorf("expected multipart/form-data: %w", err)
	}

	var (
		meta      ingest.Meta
		haveMet   bool
		tmp       *os.File
		kind      ingest.Kind
		haveCnt   bool
		isArchive bool
	)
	cleanup := func() {
		if tmp != nil {
			name := tmp.Name()
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return ingest.Meta{}, 0, nil, noop, fmt.Errorf("read part: %w", err)
		}
		switch part.FormName() {
		case "meta":
			if err := json.NewDecoder(io.LimitReader(part, 1<<20)).Decode(&meta); err != nil {
				cleanup()
				return ingest.Meta{}, 0, nil, noop, fmt.Errorf("invalid meta JSON: %w", err)
			}
			haveMet = true
		case "bundle", "index":
			if haveCnt {
				cleanup()
				return ingest.Meta{}, 0, nil, noop, fmt.Errorf("provide exactly one of bundle or index")
			}
			if part.FormName() == "bundle" {
				isArchive = true
			} else {
				kind = ingest.KindIndex
			}
			tmp, err = spoolToTemp(part)
			if err != nil {
				cleanup()
				return ingest.Meta{}, 0, nil, noop, err
			}
			haveCnt = true
		default:
			// Ignore unknown parts.
		}
		_ = part.Close()
	}

	if !haveMet {
		cleanup()
		return ingest.Meta{}, 0, nil, noop, fmt.Errorf("missing meta part")
	}
	if !haveCnt {
		cleanup()
		return ingest.Meta{}, 0, nil, noop, fmt.Errorf("missing bundle or index part")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return ingest.Meta{}, 0, nil, noop, err
	}
	if isArchive {
		kind, err = sniffArchiveKind(tmp)
		if err != nil {
			cleanup()
			return ingest.Meta{}, 0, nil, noop, err
		}
	}
	return meta, kind, tmp, cleanup, nil
}

// sniffArchiveKind inspects the leading magic bytes of a spooled bundle to tell
// gzip-tar from zip, then rewinds. The form field name is not trusted.
func sniffArchiveKind(f *os.File) (ingest.Kind, error) {
	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	switch {
	case n >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return ingest.KindBundle, nil
	case n >= 4 && magic[0] == 'P' && magic[1] == 'K' && magic[2] == 0x03 && magic[3] == 0x04:
		return ingest.KindZip, nil
	default:
		return 0, fmt.Errorf("unsupported bundle format: expected a .tar.gz or .zip archive")
	}
}

// spoolToTemp buffers a multipart part to a temp file so it can be re-read.
func spoolToTemp(part *multipart.Part) (*os.File, error) {
	f, err := os.CreateTemp("", "gotifacts-upload-*")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(f, part); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

// normalizeGroup trims and lowercases a group string for comparison.
func normalizeGroup(group string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(group)), "/")
}

// removeSiteDir deletes a site's on-disk directory (best effort).
func (s *Server) removeSiteDir(group, slug string) {
	dir := slug
	if group != "" {
		dir = group + "/" + slug
	}
	_ = os.RemoveAll(filepath.Join(s.cfg.SitesDir(), filepath.FromSlash(dir)))
}
