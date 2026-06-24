// Package logging builds the process-wide structured logger from configuration.
//
// gotifacts logs to stdout (the natural place for a container; `docker logs` and
// Portainer surface it) via the standard library's log/slog. The level and
// encoding are configurable: a human-readable text format by default — easiest
// to scan in Portainer's log view — or JSON for machine ingestion.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel maps a case-insensitive level name to a slog.Level. Recognized
// names are debug, info, warn, and error.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "": // empty defaults to info
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q (want debug, info, warn, or error)", name)
	}
}

// ValidFormat reports whether format names a supported encoding.
func ValidFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "text", "json":
		return true
	default:
		return false
	}
}

// New builds a slog.Logger writing to w at the given level in the given format
// ("text" or "json"). An unrecognized level falls back to info and an
// unrecognized format falls back to text, so logging is never silently disabled.
func New(w io.Writer, level, format string) *slog.Logger {
	lvl, _ := ParseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	default:
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}
