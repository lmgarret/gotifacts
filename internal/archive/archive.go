// Package archive safely extracts site bundles, defending against zip-slip,
// tar-bombs, symlink escapes, and oversized payloads.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Limits bounds an extraction operation.
type Limits struct {
	// MaxBytes caps total decompressed bytes written across all entries.
	MaxBytes int64
	// MaxEntries caps the number of entries processed.
	MaxEntries int
}

// Errors returned during extraction.
var (
	ErrTooManyEntries = errors.New("archive exceeds entry limit")
	ErrTooLarge       = errors.New("archive exceeds size limit")
	ErrUnsafePath     = errors.New("archive entry escapes target directory")
	ErrUnsupported    = errors.New("unsupported archive entry type")
	ErrNoIndex        = errors.New("archive does not contain a top-level index.html")
)

// ExtractTarGz extracts a gzip-compressed tar stream into dest, enforcing limits
// and rejecting unsafe paths. dest must already exist. It returns an error if no
// top-level index.html is present.
func ExtractTarGz(r io.Reader, dest string, lim Limits) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gz)
	var (
		entries  int
		written  int64
		hasIndex bool
	)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		entries++
		if entries > lim.MaxEntries {
			return ErrTooManyEntries
		}

		target, err := safeJoin(absDest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if cleanRel(hdr.Name) == "index.html" {
				hasIndex = true
			}
			n, err := writeFile(tr, target, &written, lim.MaxBytes)
			if err != nil {
				return err
			}
			written = n
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("%w: %s", ErrUnsupported, hdr.Name)
		default:
			// Skip other types (e.g. char/block devices, fifos) silently.
			continue
		}
	}
	if !hasIndex {
		return ErrNoIndex
	}
	return nil
}

// cleanRel returns the cleaned, slash-normalized relative form of name.
func cleanRel(name string) string {
	return filepath.ToSlash(filepath.Clean("/" + strings.ReplaceAll(name, `\`, "/")))[1:]
}

// safeJoin cleans name and joins it onto absDest, guaranteeing the result stays
// within absDest. Absolute paths and ".." traversal are rejected.
func safeJoin(absDest, name string) (string, error) {
	norm := strings.ReplaceAll(name, `\`, "/")
	// Reject absolute paths and any traversal component outright.
	if filepath.IsAbs(name) || strings.HasPrefix(norm, "/") {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, name)
	}
	for _, part := range strings.Split(norm, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: %s", ErrUnsafePath, name)
		}
	}
	rel := cleanRel(name)
	if rel == "" || rel == "." {
		return absDest, nil
	}
	target := filepath.Join(absDest, rel)
	if target != absDest && !strings.HasPrefix(target, absDest+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, name)
	}
	return target, nil
}

// writeFile streams src into target, enforcing the cumulative byte cap.
func writeFile(src io.Reader, target string, written *int64, maxBytes int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return *written, err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return *written, err
	}
	defer func() { _ = f.Close() }()

	remaining := maxBytes - *written
	if remaining < 0 {
		remaining = 0
	}
	// Read one extra byte to detect overflow past the limit.
	n, err := io.Copy(f, io.LimitReader(src, remaining+1))
	total := *written + n
	if err != nil {
		return total, err
	}
	if total > maxBytes {
		return total, ErrTooLarge
	}
	return total, nil
}

// WriteSingleIndex writes a single HTML document as index.html into dest.
func WriteSingleIndex(r io.Reader, dest string, maxBytes int64) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var written int64
	if _, err := writeFile(r, filepath.Join(dest, "index.html"), &written, maxBytes); err != nil {
		return err
	}
	return nil
}
