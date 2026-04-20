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
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'claude' 'start'"
          }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func runDoctor(t *testing.T, projectDir string) doctorReport {
	t.Helper()
	initStub := &storeManagementUsecaseStub{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(initStub)).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
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
