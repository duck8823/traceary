package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalizeUsesConfigLanguageWhenEnvUnset(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "")
	if err := os.Unsetenv(cliLanguageEnvKey); err != nil {
		t.Fatal(err)
	}
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"ui":{"language":"ja"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Localize("English", "日本語"); got != "日本語" {
		t.Fatalf("Localize() = %q, want Japanese from config", got)
	}
}

func TestLocalizeEnvOverridesConfigLanguage(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"ui":{"language":"ja"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cliLanguageEnvKey, "en")

	if got := Localize("English", "日本語"); got != "English" {
		t.Fatalf("Localize() = %q, want env override to keep English", got)
	}
}
