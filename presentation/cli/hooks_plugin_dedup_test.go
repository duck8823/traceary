package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestHooksInstall_SkipsWhenClaudePluginActive verifies that
// `traceary hooks install --client claude` does not touch
// settings.json when the Claude Code plugin for Traceary is already
// enabled in the user's global settings — writing both produces double
// audit records for every tool call.
func TestHooksInstall_SkipsWhenClaudePluginActive(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	writeClaudeGlobalSettings(t, homeDir, `{
  "enabledPlugins": {
    "traceary@traceary-plugins": true
  }
}`)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "claude",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("settings.json should not be written when plugin is active; stat err = %v", err)
	}
	if !strings.Contains(stdout.String(), "traceary@traceary-plugins") {
		t.Errorf("stdout = %q; want plugin-skip notice naming the plugin", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--force") {
		t.Errorf("stdout = %q; want hint about --force override", stdout.String())
	}
}

// TestHooksInstall_ForceOverridesClaudePluginDetection asserts that
// `--force` lets advanced users install hooks into settings.json even
// when the plugin is active (e.g. while developing the plugin itself).
func TestHooksInstall_ForceOverridesClaudePluginDetection(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	writeClaudeGlobalSettings(t, homeDir, `{
  "enabledPlugins": {
    "traceary@traceary-plugins": true
  }
}`)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "claude",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--force",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json should be written under --force; stat err = %v", err)
	}
}

// TestHooksInstall_InstallsWhenClaudePluginMissing documents that the
// default (non-plugin) flow still works unchanged: if the user does not
// have a Claude plugin enabled, install should write the settings file.
func TestHooksInstall_InstallsWhenClaudePluginMissing(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	// no ~/.claude/settings.json is written in homeDir
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "claude",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json should be written when plugin is not enabled; stat err = %v", err)
	}
}

func writeClaudeGlobalSettings(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
