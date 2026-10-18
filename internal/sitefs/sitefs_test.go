package sitefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rev")
	tests := []struct {
		rel  string
		want string
	}{
		{"", root},
		{"/", root},
		{"index.html", filepath.Join(root, "index.html")},
		{"/a/b.txt", filepath.Join(root, "a", "b.txt")},
		{"a/../b.txt", filepath.Join(root, "b.txt")},
		// Traversal is neutralised: ".." cannot climb above the root.
		{"../../etc/passwd", filepath.Join(root, "etc", "passwd")},
		{"/../x", filepath.Join(root, "x")},
	}
	for _, tc := range tests {
		got, err := SafeJoin(root, tc.rel)
		if err != nil {
			t.Errorf("SafeJoin(%q): %v", tc.rel, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SafeJoin(%q) = %q, want %q", tc.rel, got, tc.want)
		}
		if got != root && !strings.HasPrefix(got, root+string(os.PathSeparator)) {
			t.Errorf("SafeJoin(%q) = %q escapes root", tc.rel, got)
		}
	}
}

func TestSafeJoinRejectsUncleanRoot(t *testing.T) {
	// A root that is not in Clean form can never prefix-match the joined
	// (cleaned) target, so it is rejected rather than trusted.
	if _, err := SafeJoin("/srv/site/../site/", "a"); !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("want ErrEscapesRoot, got %v", err)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type walked struct {
	Rel  string
	Size int64
}

func collect(t *testing.T, root, prefix string) ([]walked, error) {
	t.Helper()
	var got []walked
	err := Walk(root, prefix, func(rel string, size int64) error {
		got = append(got, walked{rel, size})
		return nil
	})
	return got, err
}

func TestWalkSortedByPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "<h1>hi</h1>")
	writeFile(t, root, "b.txt", "bb")
	writeFile(t, root, "a/z.txt", "z")
	writeFile(t, root, "a/c/d.txt", "ddd")
	writeFile(t, root, "a-b.txt", "x")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := collect(t, root, "")
	if err != nil {
		t.Fatal(err)
	}
	// Plain string order of the slash paths ("a-b.txt" < "a/c/d.txt"), which
	// differs from directory-by-directory order.
	want := []walked{
		{"a-b.txt", 1},
		{"a/c/d.txt", 3},
		{"a/z.txt", 1},
		{"b.txt", 2},
		{"index.html", 11},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWalkPrefix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "x")
	writeFile(t, root, "docs/a.md", "a")
	writeFile(t, root, "docs/sub/b.md", "bb")
	writeFile(t, root, "docsx/c.md", "c")

	for _, prefix := range []string{"docs", "docs/", "/docs/"} {
		got, err := collect(t, root, prefix)
		if err != nil {
			t.Fatalf("prefix %q: %v", prefix, err)
		}
		want := []walked{{"docs/a.md", 1}, {"docs/sub/b.md", 2}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix %q: got %v, want %v", prefix, got, want)
		}
	}
}

func TestWalkPrefixErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/a.md", "a")
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", "s")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, prefix := range []string{"..", "../", "docs/../..", "../" + filepath.Base(outside), "a/../../x"} {
		if _, err := collect(t, root, prefix); !errors.Is(err, ErrEscapesRoot) {
			t.Errorf("prefix %q: want ErrEscapesRoot, got %v", prefix, err)
		}
	}
	// A symlinked prefix would walk outside the root.
	if _, err := collect(t, root, "link"); !errors.Is(err, ErrEscapesRoot) {
		t.Errorf("symlink prefix: want ErrEscapesRoot, got %v", err)
	}
	if _, err := collect(t, root, "link/sub"); err == nil {
		t.Error("prefix through symlink: want error")
	}
	if _, err := collect(t, root, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing prefix: want ErrNotExist, got %v", err)
	}
	if _, err := collect(t, root, "docs/a.md"); err == nil {
		t.Error("file prefix: want error")
	}
}

func TestWalkSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "real.txt", "r")
	writeFile(t, root, "dir/inner.txt", "i")
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", "s")

	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "file-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}

	got, err := collect(t, root, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []walked{{"dir/inner.txt", 1}, {"real.txt", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWalkCallbackError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "a")
	writeFile(t, root, "b.txt", "b")
	stop := errors.New("stop")
	n := 0
	err := Walk(root, "", func(string, int64) error {
		n++
		return stop
	})
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("err=%v calls=%d, want stop after 1 call", err, n)
	}
}

func TestIsText(t *testing.T) {
	pngHeader := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01")
	tests := []struct {
		name string
		in   []byte
		ok   bool
		ct   string // prefix of the expected content type
	}{
		{"empty", nil, true, "text/plain"},
		{"html", []byte("<!DOCTYPE html><html><body>hi</body></html>"), true, "text/html"},
		{"markdown", []byte("# Title\n\nSome *markdown* text.\n"), true, "text/plain"},
		{"json", []byte(`{"a": [1, 2, 3], "b": "c"}`), true, "text/plain"},
		{"xml", []byte(`<?xml version="1.0"?><root/>`), true, "text/xml"},
		{"utf8 multibyte", []byte("héllo wörld ✓"), true, "text/plain"},
		{"png", pngHeader, false, "image/png"},
		{"nul bytes", []byte("abc\x00def"), false, "application/octet-stream"},
		{"invalid utf8", []byte("hello \xff\xfe world"), false, "text/plain"},
		{"invalid utf8 at end", []byte("hello \xff"), false, "text/plain"},
		{"stray continuation at end", []byte("hello\x80"), false, "text/plain"},
		{"truncated 2-byte rune", []byte("caf\xc3"), true, "text/plain"},
		{"truncated 3-byte rune", []byte("ok \xe2\x9c"), true, "text/plain"},
		{"truncated 4-byte rune", []byte("ok \xf0\x9f\x98"), true, "text/plain"},
		{"complete 4-byte rune", []byte("ok \xf0\x9f\x98\x80"), true, "text/plain"},
		{"invalid in middle of long input", append(append([]byte(strings.Repeat("a", 600)), 0xff), "tail"...), false, "text/plain"},
	}
	for _, tc := range tests {
		ct, ok := IsText(tc.in)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v (content type %q)", tc.name, ok, tc.ok, ct)
		}
		if !strings.HasPrefix(ct, tc.ct) {
			t.Errorf("%s: content type %q, want prefix %q", tc.name, ct, tc.ct)
		}
	}
}
