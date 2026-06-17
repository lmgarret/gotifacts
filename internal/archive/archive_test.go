package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarGz builds a gzip tar from a list of (name, body) files.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func defaultLimits() Limits { return Limits{MaxBytes: 10 << 20, MaxEntries: 1000} }

func TestExtractTarGzHappy(t *testing.T) {
	data := makeTarGz(t, map[string]string{
		"index.html":     "<h1>hi</h1>",
		"assets/app.css": "body{}",
	})
	dest := t.TempDir()
	if err := ExtractTarGz(bytes.NewReader(data), dest, defaultLimits()); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Fatalf("index.html missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "assets", "app.css")); err != nil {
		t.Fatalf("asset missing: %v", err)
	}
}

func TestExtractTarGzNoIndex(t *testing.T) {
	data := makeTarGz(t, map[string]string{"page.html": "x"})
	if err := ExtractTarGz(bytes.NewReader(data), t.TempDir(), defaultLimits()); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("want ErrNoIndex, got %v", err)
	}
}

func TestZipSlipRejected(t *testing.T) {
	for _, name := range []string{"../evil.html", "../../etc/passwd", "a/../../escape"} {
		data := makeTarGz(t, map[string]string{name: "x", "index.html": "y"})
		dest := t.TempDir()
		err := ExtractTarGz(bytes.NewReader(data), dest, defaultLimits())
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("entry %q: want ErrUnsafePath, got %v", name, err)
		}
		// Ensure nothing escaped.
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "evil.html")); statErr == nil {
			t.Fatalf("file escaped dest for entry %q", name)
		}
	}
}

func TestSymlinkRejected(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.Close()
	_ = gz.Close()
	if err := ExtractTarGz(&buf, t.TempDir(), defaultLimits()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestTarBombEntries(t *testing.T) {
	files := map[string]string{"index.html": "x"}
	for i := 0; i < 50; i++ {
		files[strings.Repeat("a", i+1)+".txt"] = "y"
	}
	err := ExtractTarGz(bytes.NewReader(makeTarGz(t, files)), t.TempDir(), Limits{MaxBytes: 10 << 20, MaxEntries: 5})
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("want ErrTooManyEntries, got %v", err)
	}
}

func TestTarBombBytes(t *testing.T) {
	big := strings.Repeat("A", 1024)
	data := makeTarGz(t, map[string]string{"index.html": "x", "big.bin": big})
	err := ExtractTarGz(bytes.NewReader(data), t.TempDir(), Limits{MaxBytes: 100, MaxEntries: 100})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

// makeZip builds a zip archive from a list of (name, body) files. A name ending
// in "/" is written as a directory entry.
func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		if strings.HasSuffix(name, "/") {
			if _, err := zw.Create(name); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func extractZipBytes(t *testing.T, data []byte, dest string, lim Limits) error {
	t.Helper()
	return ExtractZip(bytes.NewReader(data), int64(len(data)), dest, lim)
}

func TestExtractZipHappy(t *testing.T) {
	data := makeZip(t, map[string]string{
		"index.html":     "<h1>hi</h1>",
		"assets/app.css": "body{}",
	})
	dest := t.TempDir()
	if err := extractZipBytes(t, data, dest, defaultLimits()); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Fatalf("index.html missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "assets", "app.css")); err != nil {
		t.Fatalf("asset missing: %v", err)
	}
}

// TestExtractZipUnwrapsSingleRoot covers the common case of zipping a folder,
// where everything lives under one top-level directory.
func TestExtractZipUnwrapsSingleRoot(t *testing.T) {
	data := makeZip(t, map[string]string{
		"site/":               "",
		"site/index.html":     "<h1>hi</h1>",
		"site/assets/app.css": "body{}",
	})
	dest := t.TempDir()
	if err := extractZipBytes(t, data, dest, defaultLimits()); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Fatalf("index.html not unwrapped to root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "assets", "app.css")); err != nil {
		t.Fatalf("asset not unwrapped: %v", err)
	}
}

// TestExtractZipMultipleRootsNoUnwrap ensures a top-level index.html alongside
// another top-level dir is left at the root (not mistaken for a wrapper).
func TestExtractZipMultipleRootsNoUnwrap(t *testing.T) {
	data := makeZip(t, map[string]string{
		"index.html":     "<h1>hi</h1>",
		"vendor/lib.css": "body{}",
	})
	dest := t.TempDir()
	if err := extractZipBytes(t, data, dest, defaultLimits()); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Fatalf("index.html missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "vendor", "lib.css")); err != nil {
		t.Fatalf("asset missing: %v", err)
	}
}

func TestExtractZipNoIndex(t *testing.T) {
	data := makeZip(t, map[string]string{"page.html": "x"})
	if err := extractZipBytes(t, data, t.TempDir(), defaultLimits()); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("want ErrNoIndex, got %v", err)
	}
}

func TestExtractZipSlipRejected(t *testing.T) {
	for _, name := range []string{"../evil.html", "../../etc/passwd"} {
		data := makeZip(t, map[string]string{name: "x", "index.html": "y"})
		dest := t.TempDir()
		if err := extractZipBytes(t, data, dest, defaultLimits()); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("entry %q: want ErrUnsafePath, got %v", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "evil.html")); statErr == nil {
			t.Fatalf("file escaped dest for entry %q", name)
		}
	}
}

func TestExtractZipBombEntries(t *testing.T) {
	files := map[string]string{"index.html": "x"}
	for i := 0; i < 50; i++ {
		files[strings.Repeat("a", i+1)+".txt"] = "y"
	}
	err := extractZipBytes(t, makeZip(t, files), t.TempDir(), Limits{MaxBytes: 10 << 20, MaxEntries: 5})
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("want ErrTooManyEntries, got %v", err)
	}
}

func TestExtractZipBombBytes(t *testing.T) {
	data := makeZip(t, map[string]string{"index.html": "x", "big.bin": strings.Repeat("A", 1024)})
	err := extractZipBytes(t, data, t.TempDir(), Limits{MaxBytes: 100, MaxEntries: 100})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestWriteSingleIndex(t *testing.T) {
	dest := t.TempDir()
	if err := WriteSingleIndex(strings.NewReader("<h1>hi</h1>"), dest, 1<<20); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "index.html"))
	if err != nil || string(b) != "<h1>hi</h1>" {
		t.Fatalf("index content = %q err=%v", b, err)
	}
}
