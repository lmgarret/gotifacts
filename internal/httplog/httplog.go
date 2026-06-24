// Package httplog provides request-logging middleware. Every HTTP request is
// logged once on completion with its method, host, path, status, response size,
// and latency, so the otherwise-silent request flow is visible in the logs.
package httplog

import (
	"log/slog"
	"net/http"
	"time"
)

// Middleware wraps next so each request is logged on completion. successLevel is
// used for responses below 400; 4xx responses are logged at Warn and 5xx at
// Error, so client and server faults stand out regardless of the base level.
func Middleware(log *slog.Logger, successLevel slog.Level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			level := successLevel
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}
			log.LogAttrs(r.Context(), level, "request",
				slog.String("method", r.Method),
				slog.String("host", r.Host),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote", r.RemoteAddr),
			)
		})
	}
}

// recorder captures the status code and response size for logging.
type recorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *recorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap exposes the wrapped writer so http.ResponseController can reach
// optional interfaces (Flusher, Hijacker, deadline setters) on the original.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush forwards to the underlying writer when it supports flushing, so
// streaming responses (e.g. the MCP SSE transport) are not buffered.
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
