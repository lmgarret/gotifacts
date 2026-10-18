// Package sitefs holds small filesystem helpers shared by the packages that
// read published site content from disk: traversal-safe path joining, a sorted
// regular-file walk, and a text/binary sniff.
package sitefs

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// sniffLen is how many leading bytes http.DetectContentType considers.
const sniffLen = 512

// ErrEscapesRoot is returned when a path would resolve outside the root it is
// joined onto.
var ErrEscapesRoot = errors.New("path escapes revision root")

// SafeJoin cleans rel and joins it under root, rejecting any result that would
// escape root (path traversal).
func SafeJoin(root, rel string) (string, error) {
	clean := filepath.Clean("/" + rel)
	target := filepath.Join(root, filepath.FromSlash(clean))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", ErrEscapesRoot
	}
	return target, nil
}

// Walk calls fn for every regular file under root, in sorted order of its
// slash-separated path relative to root. Directories and symlinks (including
// symlinked directories) are skipped, never followed.
//
// A non-empty prefix restricts the walk to that subdirectory of root. The
// prefix must be traversal-safe: it is rejected with ErrEscapesRoot when it
// contains a ".." segment or passes through a symlink. A prefix that does not
// exist yields an error wrapping fs.ErrNotExist; one that is not a directory
// yields an error. The rel passed to fn is always relative to root, so it
// includes the prefix.
//
// The walk collects all matching files before calling fn. If fn returns an
// error the walk stops and returns it unchanged.
func Walk(root, prefix string, fn func(rel string, size int64) error) error {
	start := root
	if p := strings.Trim(filepath.ToSlash(prefix), "/"); p != "" {
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return ErrEscapesRoot
			}
		}
		var err error
		if start, err = SafeJoin(root, p); err != nil {
			return err
		}
		if err := checkPrefixDir(root, start); err != nil {
			return err
		}
	}

	type entry struct {
		rel  string
		size int64
	}
	var entries []entry
	err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() { // directories, symlinks, devices, ...
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{rel: filepath.ToSlash(rel), size: info.Size()})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	for _, e := range entries {
		if err := fn(e.rel, e.size); err != nil {
			return err
		}
	}
	return nil
}

// checkPrefixDir verifies that dir (already joined under root) exists, is a
// directory, and that no component below root is a symlink.
func checkPrefixDir(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return ErrEscapesRoot
	}
	cur := root
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return ErrEscapesRoot
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s: not a directory", rel)
		}
	}
	return nil
}

// textMIMEs are the non-text/* media types that carry textual content.
var textMIMEs = map[string]bool{
	"application/json":         true,
	"application/ld+json":      true,
	"application/xml":          true,
	"application/xhtml+xml":    true,
	"application/javascript":   true,
	"application/x-javascript": true,
	"image/svg+xml":            true,
}

// IsText reports whether head, the leading bytes of a file, looks like text.
// It sniffs at most the first 512 bytes with http.DetectContentType and
// returns that content type. ok is true when the type is textual (text/*, JSON,
// XML, JavaScript, SVG) and head is valid UTF-8. A multi-byte rune cut off at
// the very end of head is tolerated, since callers pass a prefix of the file.
// Empty input counts as text.
func IsText(head []byte) (contentType string, ok bool) {
	sniff := head
	if len(sniff) > sniffLen {
		sniff = sniff[:sniffLen]
	}
	contentType = http.DetectContentType(sniff)
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = contentType
	}
	if !strings.HasPrefix(mt, "text/") && !textMIMEs[mt] {
		return contentType, false
	}
	return contentType, utf8.Valid(trimPartialRune(head))
}

// trimPartialRune drops an incomplete multi-byte rune at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}
