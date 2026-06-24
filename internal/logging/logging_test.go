package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]struct {
		want    slog.Level
		wantErr bool
	}{
		"debug":   {slog.LevelDebug, false},
		"INFO":    {slog.LevelInfo, false},
		"":        {slog.LevelInfo, false},
		"warn":    {slog.LevelWarn, false},
		"warning": {slog.LevelWarn, false},
		"error":   {slog.LevelError, false},
		"bogus":   {slog.LevelInfo, true},
	}
	for name, tc := range cases {
		got, err := ParseLevel(name)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseLevel(%q) err=%v, wantErr=%v", name, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", name, got, tc.want)
		}
	}
}

func TestValidFormat(t *testing.T) {
	for _, f := range []string{"text", "json", "TEXT", "JSON"} {
		if !ValidFormat(f) {
			t.Errorf("ValidFormat(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"", "yaml", "logfmt"} {
		if ValidFormat(f) {
			t.Errorf("ValidFormat(%q) = true, want false", f)
		}
	}
}

func TestNewJSONFormatAndLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "warn", "json")

	log.Info("suppressed below threshold")
	if buf.Len() != 0 {
		t.Fatalf("info line emitted at warn level: %q", buf.String())
	}

	log.Warn("kept", "k", "v")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}
	if rec["msg"] != "kept" || rec["k"] != "v" {
		t.Errorf("unexpected JSON record: %v", rec)
	}
}

func TestNewDefaultsToText(t *testing.T) {
	var buf bytes.Buffer
	// Unknown format falls back to text rather than disabling logging.
	New(&buf, "info", "bogus").Info("hello", "k", "v")
	out := buf.String()
	if !strings.Contains(out, "msg=hello") || !strings.Contains(out, "k=v") {
		t.Errorf("expected text output, got %q", out)
	}
}
