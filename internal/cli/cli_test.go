package cli

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigDirRespectsEnvOverride(t *testing.T) {
	t.Setenv("BRAMBLEGATE_CONFIG_DIR", "/tmp/custom-bramblegate")
	if got := defaultConfigDir(); got != "/tmp/custom-bramblegate" {
		t.Fatalf("got %q, want env override", got)
	}
}

// TestDefaultConfigDirOutsideContainer asserts the host (non-container)
// fallback: an OS-conventional config directory, not the literal "/config"
// (which on Windows resolves to the current drive's root, e.g. C:\config —
// the bug this default exists to avoid).
func TestDefaultConfigDirOutsideContainer(t *testing.T) {
	if inContainer() {
		t.Skip("running inside a container; this test asserts the non-container fallback")
	}
	t.Setenv("BRAMBLEGATE_CONFIG_DIR", "")

	want, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no OS config directory available in this environment")
	}
	want = filepath.Join(want, "bramblegate")

	if got := defaultConfigDir(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDefaultLogLevelRespectsEnvOverride(t *testing.T) {
	t.Setenv("BRAMBLEGATE_LOG_LEVEL", "debug")
	if got := defaultLogLevel(); got != "debug" {
		t.Fatalf("got %q, want env override", got)
	}
}

func TestDefaultLogLevelDefaultsToInfo(t *testing.T) {
	t.Setenv("BRAMBLEGATE_LOG_LEVEL", "")
	if got := defaultLogLevel(); got != "info" {
		t.Fatalf("got %q, want %q", got, "info")
	}
}

func TestDefaultLogFormatRespectsEnvOverride(t *testing.T) {
	t.Setenv("BRAMBLEGATE_LOG_FORMAT", "text")
	if got := defaultLogFormat(); got != "text" {
		t.Fatalf("got %q, want env override", got)
	}
}

func TestDefaultLogFormatDefaultsToJSON(t *testing.T) {
	t.Setenv("BRAMBLEGATE_LOG_FORMAT", "")
	if got := defaultLogFormat(); got != "json" {
		t.Fatalf("got %q, want %q", got, "json")
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for in, want := range cases {
		got, err := parseLogLevel(in)
		if err != nil {
			t.Fatalf("parseLogLevel(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseLogLevelRejectsUnknown(t *testing.T) {
	if _, err := parseLogLevel("verbose"); err == nil {
		t.Fatal("expected an error for an unknown log level")
	}
}

func TestNewLoggerRejectsUnknownFormat(t *testing.T) {
	if _, err := newLogger("info", "xml"); err == nil {
		t.Fatal("expected an error for an unknown log format")
	}
}

func TestNewLoggerAcceptsJSONAndText(t *testing.T) {
	if _, err := newLogger("debug", "json"); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, err := newLogger("debug", "text"); err != nil {
		t.Fatalf("text: %v", err)
	}
}
