package presentation_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/presentation"
)

func TestLoadConfig_ファイルが存在しない場合はゼロ値を返す(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	config := presentation.LoadConfig()

	if len(config.Redact.ExtraPatterns) != 0 {
		t.Errorf("expected empty extra patterns, got %v", config.Redact.ExtraPatterns)
	}
}

func TestLoadConfig_不正なJSONの場合はゼロ値を返す(t *testing.T) {
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
}

func TestLoadConfig_正常なconfig_jsonからパターンを読み込める(t *testing.T) {
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
}
