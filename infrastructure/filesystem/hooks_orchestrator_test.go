package filesystem_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func newTestOrchestrator(homeDir string) *filesystem.HooksOrchestrator {
	return filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
		"claude": filesystem.NewClaudeHooksHandler(),
		"codex": filesystem.NewCodexHooksHandlerWithHomeDirFunc(func() (string, error) {
			return homeDir, nil
		}),
		"gemini": filesystem.NewGeminiHooksHandler(),
	})
}

func TestHooksOrchestrator_GenerateReturnsClientSpecificJSON(t *testing.T) {
	t.Parallel()

	orchestrator := newTestOrchestrator(t.TempDir())

	encoded, err := orchestrator.Generate(context.Background(), "claude", "/scripts", "traceary")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := root["hooks"]; !ok {
		t.Fatalf("Generate() output missing hooks field: %s", encoded)
	}
}

func TestHooksOrchestrator_GenerateHandlesAliases(t *testing.T) {
	t.Parallel()

	orchestrator := newTestOrchestrator(t.TempDir())

	aliases := []string{"claude-code", "codex-cli", "gemini-cli"}
	for _, alias := range aliases {
		t.Run(alias, func(t *testing.T) {
			t.Parallel()

			if _, err := orchestrator.Generate(context.Background(), alias, "/scripts", "traceary"); err != nil {
				t.Fatalf("Generate(%q) error = %v", alias, err)
			}
		})
	}
}

func TestHooksOrchestrator_GenerateRejectsUnknownClient(t *testing.T) {
	t.Parallel()

	orchestrator := newTestOrchestrator(t.TempDir())

	_, err := orchestrator.Generate(context.Background(), "unknown", "/scripts", "traceary")
	if err == nil {
		t.Fatalf("Generate() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "valid values: claude, codex, gemini") {
		t.Fatalf("error = %q, want valid values hint", err.Error())
	}
}

func TestHooksOrchestrator_InstallWritesToStandardPath(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	resolved, err := orchestrator.Install(
		context.Background(),
		"claude",
		"/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		false,
	)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if got, want := resolved, filepath.Join(projectDir, ".claude", "settings.json"); got != want {
		t.Fatalf("Install() = %q, want %q", got, want)
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", resolved, err)
	}
	if !strings.Contains(string(content), "SessionStart") {
		t.Fatalf("Install() output missing SessionStart: %s", content)
	}
}

func TestHooksOrchestrator_InstallMergesExistingFile(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	settingsPath := filepath.Join(projectDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existing := []byte(`{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "echo custom"
          },
          {
            "name": "traceary-session-start",
            "type": "command",
            "command": "bash '/old/scripts/traceary-session.sh' 'gemini' 'start'"
          }
        ]
      }
    ]
  }
}
`)
	if err := os.WriteFile(settingsPath, existing, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := orchestrator.Install(
		context.Background(),
		"gemini",
		"/new/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		false,
	); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), `"theme": "dark"`) {
		t.Fatalf("merged settings lost unrelated top-level field: %s", content)
	}
	if !strings.Contains(string(content), `"command": "echo custom"`) {
		t.Fatalf("merged settings lost custom hook: %s", content)
	}
	if strings.Count(string(content), "traceary-session-start") != 1 {
		t.Fatalf("merged settings should contain exactly one traceary-session-start: %s", content)
	}
	if strings.Contains(string(content), "/old/scripts") {
		t.Fatalf("merged settings kept old scripts path: %s", content)
	}
}

func TestHooksOrchestrator_InstallForceOverwrites(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	settingsPath := filepath.Join(projectDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"existing":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := orchestrator.Install(
		context.Background(),
		"gemini",
		"/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		true,
	); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(content), `"existing":true`) {
		t.Fatalf("force install did not overwrite existing content: %s", content)
	}
}

func TestHooksOrchestrator_InstallUsesCodexHomeDir(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	orchestrator := newTestOrchestrator(homeDir)

	resolved, err := orchestrator.Install(
		context.Background(),
		"codex",
		"/scripts",
		"traceary",
		projectDir,
		types.Empty[string](),
		false,
	)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if got, want := resolved, filepath.Join(homeDir, ".codex", "hooks.json"); got != want {
		t.Fatalf("Install() = %q, want %q", got, want)
	}
}

func TestHooksOrchestrator_SupportedClients(t *testing.T) {
	t.Parallel()

	orchestrator := newTestOrchestrator(t.TempDir())

	if diff := cmp.Diff([]string{"claude", "codex", "gemini"}, orchestrator.SupportedClients()); diff != "" {
		t.Fatalf("SupportedClients() mismatch (-want +got):\n%s", diff)
	}
}

func TestHooksOrchestrator_NormalizeClient(t *testing.T) {
	t.Parallel()

	orchestrator := newTestOrchestrator(t.TempDir())

	tests := []struct {
		input string
		want  string
	}{
		{"claude", "claude"},
		{"claude-code", "claude"},
		{"codex-cli", "codex"},
		{"gemini-cli", "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got, err := orchestrator.NormalizeClient(tt.input)
			if err != nil {
				t.Fatalf("NormalizeClient(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeClient(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHooksOrchestrator_ResolveInstallPathHonorsOverride(t *testing.T) {
	t.Parallel()

	orchestrator := newTestOrchestrator(t.TempDir())

	override := filepath.Join(t.TempDir(), "custom", "settings.json")
	resolved, err := orchestrator.ResolveInstallPath("claude", "/project", types.Of(override))
	if err != nil {
		t.Fatalf("ResolveInstallPath() error = %v", err)
	}
	if resolved != override {
		t.Fatalf("ResolveInstallPath() = %q, want %q", resolved, override)
	}
}
