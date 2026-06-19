package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_Doctor_ClaudePluginInteractions verifies the four
// combinations of (plugin active/inactive) × (settings.json has
// traceary hooks or not) that doctor must classify:
//
//   - plugin active + settings with hooks  → warn (double registration)
//   - plugin active + settings without     → pass (plugin-managed)
//   - plugin inactive + settings with      → pass (standard install)
//   - plugin inactive + settings without   → warn (needs install)
func TestRootCLI_Doctor_ClaudePluginInteractions(t *testing.T) {
	t.Run("plugin active with traceary hooks in settings warns about duplicates", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writePluginEnabledSettings(t, homeDir)
		writeClaudeProjectHook(t, projectDir)

		report := runDoctor(t, projectDir)
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "warn" {
			t.Fatalf("claude-config status = %q, want warn; msg = %q", claudeCfg.Status, claudeCfg.Message)
		}
		if !strings.Contains(claudeCfg.Message, "twice") && !strings.Contains(claudeCfg.Message, "二重") {
			t.Errorf("claude-config message = %q; want double-registration hint", claudeCfg.Message)
		}
		if claudeCfg.AutoFixAvailable {
			t.Fatalf("claude-config AutoFixAvailable = true, want guided-only remediation")
		}
		if !strings.Contains(claudeCfg.Hint, "remove") && !strings.Contains(claudeCfg.Hint, "disable") {
			t.Fatalf("claude-config hint = %q, want manual choice hint", claudeCfg.Hint)
		}
	})

	t.Run("plugin active without settings hooks passes", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writePluginEnabledSettings(t, homeDir)

		report := runDoctor(t, projectDir)
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "pass" {
			t.Fatalf("claude-config status = %q, want pass; msg = %q", claudeCfg.Status, claudeCfg.Message)
		}
		if !strings.Contains(claudeCfg.Message, "plugin") {
			t.Errorf("claude-config message = %q; want mention of plugin", claudeCfg.Message)
		}
	})

	t.Run("plugin inactive with settings hooks passes", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writeClaudeProjectHook(t, projectDir)

		report := runDoctor(t, projectDir)
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "pass" {
			t.Fatalf("claude-config status = %q, want pass; msg = %q", claudeCfg.Status, claudeCfg.Message)
		}
	})

	t.Run("plugin inactive without settings hooks warns about missing install", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		report := runDoctor(t, projectDir)
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "warn" {
			t.Fatalf("claude-config status = %q, want warn; msg = %q", claudeCfg.Status, claudeCfg.Message)
		}
	})

	// Plugin activity must not mask a malformed project settings file —
	// Claude Code itself would reject it, so `doctor` keeps reporting it
	// as fail even when the plugin would otherwise cover the hooks.
	t.Run("plugin active with malformed settings still fails", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writePluginEnabledSettings(t, homeDir)
		writeRawProjectSettings(t, projectDir, "{ not json")

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

		// The command may succeed or fail at the Run level; what matters is
		// that the report surfaces the broken config, not that the process
		// panics.
		_ = rootCmd.Execute()
		report := decodeDoctorReport(t, stdout.Bytes())
		claudeCfg := statusByName(report, "claude-config")
		if claudeCfg.Status != "fail" {
			t.Fatalf("claude-config status = %q, want fail; msg = %q", claudeCfg.Status, claudeCfg.Message)
		}
	})
}

// TestRootCLI_Doctor_ClaudeGlobalConfig asserts that doctor reports the
// state of the user-level ~/.claude/settings.json as a separate check
// alongside the project-level claude-config check.
func TestRootCLI_Doctor_ClaudeGlobalConfig(t *testing.T) {
	t.Run("reports global config with traceary hooks as pass", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writeClaudeGlobalHooksSettings(t, homeDir)

		report := runDoctor(t, projectDir)
		globalCfg := statusByName(report, "claude-global-config")
		if globalCfg.Status != "pass" {
			t.Fatalf("claude-global-config status = %q, want pass; msg = %q", globalCfg.Status, globalCfg.Message)
		}
		if !strings.Contains(globalCfg.Message, "every project") && !strings.Contains(globalCfg.Message, "全プロジェクト") {
			t.Errorf("claude-global-config message = %q; want mention of global scope", globalCfg.Message)
		}
	})

	t.Run("reports missing global config as skip", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		report := runDoctor(t, projectDir)
		globalCfg := statusByName(report, "claude-global-config")
		if globalCfg.Status != "skip" {
			t.Fatalf("claude-global-config status = %q, want skip; msg = %q", globalCfg.Status, globalCfg.Message)
		}
	})

	t.Run("reports malformed global config as fail", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		dir := filepath.Join(homeDir, ".claude")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{ not json"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		initStub := &storeManagementUsecaseStub{}
		rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})
		_ = rootCmd.Execute()

		report := decodeDoctorReport(t, stdout.Bytes())
		globalCfg := statusByName(report, "claude-global-config")
		if globalCfg.Status != "fail" {
			t.Fatalf("claude-global-config status = %q, want fail; msg = %q", globalCfg.Status, globalCfg.Message)
		}
	})
}

// TestRootCLI_Doctor_GeminiGlobalConfig mirrors the Claude-global tests
// for Gemini: its user-level settings also live under $HOME, and the
// shared inspector emits the `gemini-global-config` check with the same
// three-state contract.
func TestRootCLI_Doctor_GeminiGlobalConfig(t *testing.T) {
	t.Run("reports gemini global config with traceary hooks as pass", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writeGeminiGlobalHooksSettings(t, homeDir)

		report := runDoctorForClient(t, "gemini", projectDir)
		globalCfg := statusByName(report, "gemini-global-config")
		if globalCfg.Status != "pass" {
			t.Fatalf("gemini-global-config status = %q, want pass; msg = %q", globalCfg.Status, globalCfg.Message)
		}
	})

	t.Run("reports partial gemini global config as warn", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		writeGeminiPartialGlobalHooksSettings(t, homeDir)

		report := runDoctorForClient(t, "gemini", projectDir)
		globalCfg := statusByName(report, "gemini-global-config")
		if globalCfg.Status != "warn" {
			t.Fatalf("gemini-global-config status = %q, want warn; msg = %q", globalCfg.Status, globalCfg.Message)
		}
		if !strings.Contains(globalCfg.Message, "prompt") || !strings.Contains(globalCfg.Message, "transcript") {
			t.Fatalf("gemini-global-config message = %q; want prompt/transcript gap", globalCfg.Message)
		}
		if !strings.Contains(globalCfg.FixCommand, "--global --upgrade") {
			t.Fatalf("gemini-global-config FixCommand = %q; want global upgrade command", globalCfg.FixCommand)
		}
	})

	t.Run("reports missing gemini global config as skip", func(t *testing.T) {
		homeDir := t.TempDir()
		projectDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
		t.Cleanup(cli.ResetUserHomeDirFunc)

		report := runDoctorForClient(t, "gemini", projectDir)
		globalCfg := statusByName(report, "gemini-global-config")
		if globalCfg.Status != "skip" {
			t.Fatalf("gemini-global-config status = %q, want skip; msg = %q", globalCfg.Status, globalCfg.Message)
		}
	})
}

// TestRootCLI_Doctor_CodexHasNoGlobalCheck documents that Codex skips
// the separate `*-global-config` line because its standard install path
// is already under $HOME — the existing `codex-config` check is enough.
func TestRootCLI_Doctor_CodexHasNoGlobalCheck(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	report := runDoctorForClient(t, "codex", projectDir)
	for _, check := range report.Checks {
		if check.Name == "codex-global-config" {
			t.Fatalf("codex doctor report should not include codex-global-config; got %+v", check)
		}
	}
}

func TestRootCLI_Doctor_CodexDuplicateHooksWarns(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	writeCodexDuplicateAuditHook(t, homeDir)

	report := runDoctorForClient(t, "codex", projectDir)
	codexCfg := statusByName(report, "codex-config")
	if codexCfg.Status != "warn" {
		t.Fatalf("codex-config status = %q, want warn; msg = %q", codexCfg.Status, codexCfg.Message)
	}
	if !strings.Contains(codexCfg.Message, "duplicate") || !strings.Contains(codexCfg.Message, "PostToolUse") {
		t.Fatalf("codex-config message = %q; want duplicate PostToolUse warning", codexCfg.Message)
	}
	if !strings.Contains(codexCfg.Hint, "--dry-run") {
		t.Fatalf("codex-config hint = %q; want dry-run-first guidance", codexCfg.Hint)
	}
	if !codexCfg.AutoFixAvailable {
		t.Fatal("codex-config AutoFixAvailable = false, want true for non-destructive doctor fix preview/apply")
	}
	if !strings.Contains(codexCfg.FixCommand, "doctor --fix --dry-run") {
		t.Fatalf("codex-config FixCommand = %q, want doctor dry-run command", codexCfg.FixCommand)
	}
}

func runDoctorForClient(t *testing.T, client, projectDir string) doctorReport {
	t.Helper()
	initStub := &storeManagementUsecaseStub{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", client, "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)
	return decodeDoctorReport(t, stdout.Bytes())
}

func writeCodexDuplicateAuditHook(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"name": "traceary-session-start", "type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'start'"}]}
    ],
    "UserPromptSubmit": [
      {"hooks": [{"name": "traceary-prompt", "type": "command", "command": "'traceary' 'hook' 'prompt' 'codex'"}]}
    ],
    "Stop": [
      {"hooks": [
        {"name": "traceary-transcript", "type": "command", "command": "'traceary' 'hook' 'transcript' 'codex'"},
        {"name": "traceary-session-stop", "type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'stop'"}
      ]}
    ],
    "PostToolUse": [
      {"matcher": "", "hooks": [
        {"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"},
        {"name": "traceary-audit", "type": "command", "command": "'traceary' 'hook' 'audit' 'codex'"},
        {"name": "user-audit", "type": "command", "command": "echo user-hook"}
      ]}
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeGeminiGlobalHooksSettings(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-session-start",
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'gemini' 'start'"
          }
        ]
      }
    ],
    "BeforeAgent": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-prompt",
            "type": "command",
            "command": "'traceary' 'hook' 'prompt' 'gemini'"
          }
        ]
      }
    ],
    "AfterAgent": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-transcript",
            "type": "command",
            "command": "'traceary' 'hook' 'transcript' 'gemini'"
          }
        ]
      }
    ],
    "AfterTool": [
      {
        "matcher": "run_shell_command",
        "hooks": [
          {
            "name": "traceary-audit",
            "type": "command",
            "command": "'traceary' 'hook' 'audit' 'gemini'"
          }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeGeminiPartialGlobalHooksSettings(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-session-start",
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'gemini' 'start'"
          }
        ]
      }
    ],
    "AfterTool": [
      {
        "matcher": "run_shell_command",
        "hooks": [
          {
            "name": "traceary-audit",
            "type": "command",
            "command": "'traceary' 'hook' 'audit' 'gemini'"
          }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeClaudeGlobalHooksSettings(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-session-start",
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'claude' 'start'"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-prompt",
            "type": "command",
            "command": "'traceary' 'hook' 'prompt' 'claude'"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "traceary-transcript",
            "type": "command",
            "command": "'traceary' 'hook' 'transcript' 'claude'"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "name": "traceary-audit",
            "type": "command",
            "command": "'traceary' 'hook' 'audit' 'claude'"
          }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeRawProjectSettings(t *testing.T, projectDir, content string) {
	t.Helper()
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writePluginEnabledSettings(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "enabledPlugins": {
    "traceary@traceary-plugins": true
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeClaudeProjectHook(t *testing.T, projectDir string) {
	t.Helper()
	writeCompleteClaudeProjectHookSettings(t, projectDir)
}

func runDoctor(t *testing.T, projectDir string) doctorReport {
	t.Helper()
	initStub := &storeManagementUsecaseStub{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

	executeDoctorAllowWarnings(t, rootCmd)
	return decodeDoctorReport(t, stdout.Bytes())
}

func statusByName(report doctorReport, name string) doctorCheck {
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	return doctorCheck{}
}

// ensure cmp is referenced so test-only import paths stay clean on older toolchains
var _ = cmp.Diff
