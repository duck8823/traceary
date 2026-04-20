package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestHooksInstall_GlobalClaudeWritesToHomeConfig asserts that
// `hooks install --client claude --global` bypasses the project
// directory and writes hooks into the user-level
// ~/.claude/settings.json file.
func TestHooksInstall_GlobalClaudeWritesToHomeConfig(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
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
		"--global",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	globalPath := filepath.Join(homeDir, ".claude", "settings.json")
	if _, err := os.Stat(globalPath); err != nil {
		t.Fatalf("global settings.json should be written; stat err = %v", err)
	}
	projectPath := filepath.Join(projectDir, ".claude", "settings.json")
	if _, err := os.Stat(projectPath); !os.IsNotExist(err) {
		t.Errorf("project settings.json should NOT be written under --global; stat err = %v", err)
	}
	if !strings.Contains(stdout.String(), globalPath) {
		t.Errorf("stdout = %q; want mention of global path %q", stdout.String(), globalPath)
	}
}

// TestHooksInstall_GlobalAndOutputMutuallyExclusive asserts that passing
// both --global and --output produces a clear error rather than silently
// preferring one.
func TestHooksInstall_GlobalAndOutputMutuallyExclusive(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "claude",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--global",
		"--output", "/tmp/should-not-be-used.json",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil; want non-nil mutual-exclusion error")
	}
	if !strings.Contains(err.Error(), "--global") || !strings.Contains(err.Error(), "--output") {
		t.Errorf("err = %v; want mention of --global and --output", err)
	}
}

// TestHooksInstall_GlobalCodexIsNoOp confirms that --global is a no-op
// for Codex because its hooks config (~/.codex/hooks.json) is already
// user-level; install should proceed with a friendly notice.
func TestHooksInstall_GlobalCodexIsNoOp(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "codex",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--global",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "user-level") && !strings.Contains(stdout.String(), "user-level 設定") && !strings.Contains(stdout.String(), "--global") {
		t.Errorf("stdout = %q; want no-op notice", stdout.String())
	}
	codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
	if _, err := os.Stat(codexPath); err != nil {
		t.Errorf("codex hooks.json should still be written; stat err = %v", err)
	}
}
