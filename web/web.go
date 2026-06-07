// Package web embeds the built management SPA (web/dist) for serving by the
// portal. The dist directory is produced by the Node build; a placeholder is
// committed so the Go module always builds.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded SPA file system rooted at dist/.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
