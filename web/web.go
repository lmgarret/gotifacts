// Package web embeds the built management SPA (web/dist) for serving by the
// portal. The dist directory is produced by the Node build (`npm run build`)
// and is gitignored; only an empty web/dist/.gitkeep placeholder is tracked so
// the Go module always builds, even without a frontend build.
package web

import (
	"embed"
	"io/fs"
)

// The `all:` prefix is load-bearing: it embeds the dotfile placeholder
// (web/dist/.gitkeep), so this compiles even when the frontend has not been
// built. Dropping `all:` would make `go build`/`go test` fail on a clean
// checkout. When the frontend is absent at runtime the portal serves a 500
// "frontend not built" (see internal/portal/spa.go).
//
//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded SPA file system rooted at dist/.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
