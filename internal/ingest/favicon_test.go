package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSite writes a set of slash-relative files into a fresh temp dir and
// returns the dir, for exercising detectFavicon directly.
func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectFaviconDeclaredFile(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": `<html><head><link rel="icon" href="/icon.svg"></head></html>`,
		"icon.svg":   `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
	})
	data, ct := detectFavicon(dir)
	if string(data) != `<svg xmlns="http://www.w3.org/2000/svg"></svg>` {
		t.Fatalf("data = %q", data)
	}
	if ct != "image/svg+xml" {
		t.Fatalf("content-type = %q, want image/svg+xml", ct)
	}
}

func TestDetectFaviconRelativeHref(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html":     `<link rel="shortcut icon" href="assets/fav.png">`,
		"assets/fav.png": "PNGDATA",
	})
	data, ct := detectFavicon(dir)
	if string(data) != "PNGDATA" || ct != "image/png" {
		t.Fatalf("got (%q, %q), want (PNGDATA, image/png)", data, ct)
	}
}

func TestDetectFaviconDataURI(t *testing.T) {
	// "hi" base64-encoded is "aGk=".
	dir := writeSite(t, map[string]string{
		"index.html": `<link rel="icon" href="data:image/png;base64,aGk=">`,
	})
	data, ct := detectFavicon(dir)
	if string(data) != "hi" {
		t.Fatalf("data = %q, want hi", data)
	}
	if ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}
}

func TestDetectFaviconIcoFallback(t *testing.T) {
	// No declared icon, but a bundled /favicon.ico exists.
	dir := writeSite(t, map[string]string{
		"index.html":  `<html><head><title>x</title></head></html>`,
		"favicon.ico": "ICODATA",
	})
	data, ct := detectFavicon(dir)
	if string(data) != "ICODATA" || ct != "image/x-icon" {
		t.Fatalf("got (%q, %q), want (ICODATA, image/x-icon)", data, ct)
	}
}

func TestDetectFaviconNone(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": `<html><head><title>nothing</title></head></html>`,
	})
	if data, ct := detectFavicon(dir); data != nil || ct != "" {
		t.Fatalf("got (%q, %q), want (nil, \"\")", data, ct)
	}
}

func TestDetectFaviconMissingDeclaredFallsBack(t *testing.T) {
	// Declared icon points at a missing file; detection falls through to .ico.
	dir := writeSite(t, map[string]string{
		"index.html":  `<link rel="icon" href="/missing.svg">`,
		"favicon.ico": "ICO",
	})
	data, ct := detectFavicon(dir)
	if string(data) != "ICO" || ct != "image/x-icon" {
		t.Fatalf("got (%q, %q), want (ICO, image/x-icon)", data, ct)
	}
}

func TestDetectFaviconPrefersSVGThenSize(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": `<head>
			<link rel="icon" type="image/png" sizes="16x16" href="/small.png">
			<link rel="icon" type="image/png" sizes="64x64" href="/big.png">
			<link rel="icon" type="image/svg+xml" href="/vec.svg">
			<link rel="apple-touch-icon" href="/apple.png">
		</head>`,
		"small.png": "S",
		"big.png":   "B",
		"vec.svg":   "V",
		"apple.png": "A",
	})
	// SVG (rel=icon) is preferred over any raster and over apple-touch-icon.
	if data, _ := detectFavicon(dir); string(data) != "V" {
		t.Fatalf("data = %q, want V (svg)", data)
	}
}

func TestDetectFaviconLargestRaster(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": `<head>
			<link rel="icon" type="image/png" sizes="16x16" href="/small.png">
			<link rel="icon" type="image/png" sizes="64x64" href="/big.png">
		</head>`,
		"small.png": "S",
		"big.png":   "B",
	})
	if data, _ := detectFavicon(dir); string(data) != "B" {
		t.Fatalf("data = %q, want B (largest)", data)
	}
}

func TestDetectFaviconIgnoresExternal(t *testing.T) {
	// An external URL cannot be cached; with nothing else, detection yields none.
	dir := writeSite(t, map[string]string{
		"index.html": `<link rel="icon" href="https://cdn.example.com/icon.png">`,
	})
	if data, ct := detectFavicon(dir); data != nil || ct != "" {
		t.Fatalf("got (%q, %q), want (nil, \"\") for external icon", data, ct)
	}
}

func TestDetectFaviconTraversalRefused(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"index.html": `<link rel="icon" href="/../secret.png">`,
	})
	// Write a sibling file outside the site root that the traversal targets.
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.png"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, _ := detectFavicon(dir); string(data) == "SECRET" {
		t.Fatal("traversal escaped the site root")
	}
}
