package presentation_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation"
)

func TestLoadConfig_returnsZeroValueWhenFileDoesNotExist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	config := presentation.LoadConfig()

	if len(config.Redact.ExtraPatterns) != 0 {
		t.Errorf("expected empty extra patterns, got %v", config.Redact.ExtraPatterns)
	}

	result := presentation.InspectConfig()
	if result.Status != presentation.ConfigLoadStatusMissing {
		t.Fatalf("InspectConfig().Status = %q, want %q", result.Status, presentation.ConfigLoadStatusMissing)
	}
}

func TestLoadConfig_returnsZeroValueForInvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{invalid}"), 0o644); err != nil {
		t.Fatal(err)
	}

	config := presentation.LoadConfig()

	if len(config.Redact.ExtraPatterns) != 0 {
		t.Errorf("expected empty extra patterns, got %v", config.Redact.ExtraPatterns)
	}

	result := presentation.InspectConfig()
	if result.Status != presentation.ConfigLoadStatusInvalid {
		t.Fatalf("InspectConfig().Status = %q, want %q", result.Status, presentation.ConfigLoadStatusInvalid)
	}
}

func TestLoadConfig_loadsPatternsFromValidConfigJSON(t *testing.T) {
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

	config := presentation.LoadConfig()

	if len(config.Redact.ExtraPatterns) != 2 {
		t.Fatalf("expected 2 extra patterns, got %d", len(config.Redact.ExtraPatterns))
	}
	if config.Redact.ExtraPatterns[0] != "my_secret" {
		t.Errorf("expected first pattern 'my_secret', got %q", config.Redact.ExtraPatterns[0])
	}
	if config.Redact.ExtraPatterns[1] != "internal_token" {
		t.Errorf("expected second pattern 'internal_token', got %q", config.Redact.ExtraPatterns[1])
	}

	result := presentation.InspectConfig()
	if result.Status != presentation.ConfigLoadStatusLoaded {
		t.Fatalf("InspectConfig().Status = %q, want %q", result.Status, presentation.ConfigLoadStatusLoaded)
	}
}

func TestLoadConfig_logsWarningForInvalidJSON(t *testing.T) {
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

	_ = presentation.LoadConfig()

	if !strings.Contains(logBuffer.String(), "Traceary config is invalid") {
		t.Fatalf("expected warning log about invalid config, got: %s", logBuffer.String())
	}
}
