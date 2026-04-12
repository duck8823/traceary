package presentation_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/presentation"
)

func TestLoadExtraRedactPatterns_returnsNilWhenFileDoesNotExist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	patterns := presentation.LoadExtraRedactPatterns()

	if len(patterns) != 0 {
		t.Errorf("expected empty extra patterns, got %v", patterns)
	}
}

func TestLoadExtraRedactPatterns_returnsNilForInvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid}"), 0o644); err != nil {
		t.Fatal(err)
	}

	patterns := presentation.LoadExtraRedactPatterns()

	if len(patterns) != 0 {
		t.Errorf("expected empty extra patterns, got %v", patterns)
	}
}

func TestLoadExtraRedactPatterns_loadsPatternsFromValidConfigJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"redact": {"extra_patterns": ["my_secret", "internal_token"]}}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	patterns := presentation.LoadExtraRedactPatterns()

	if diff := cmp.Diff([]string{"my_secret", "internal_token"}, patterns); diff != "" {
		t.Fatalf("LoadExtraRedactPatterns() mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadExtraRedactPatterns_logsWarningForInvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid}"), 0o644); err != nil {
		t.Fatal(err)
	}

	logBuffer := &bytes.Buffer{}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuffer, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})

	_ = presentation.LoadExtraRedactPatterns()

	if !strings.Contains(logBuffer.String(), "Traceary config is invalid") {
		t.Fatalf("expected warning log about invalid config, got: %s", logBuffer.String())
	}
}
