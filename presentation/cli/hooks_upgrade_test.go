package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestHooksInstall_UpgradeAddsMissingEventsAndReports asserts that
// `traceary hooks install --client codex --upgrade` on a config that
// already carries the SessionStart hook but not the newer
// UserPromptSubmit event adds the missing coverage and prints an
// "Added" line for the migrated event. This mirrors the dogfooding
// scenario that drove #637.
func TestHooksInstall_UpgradeAddsMissingEventsAndReports(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existing := []byte(`{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "'traceary' 'hook' 'session' 'codex' 'start'"}]}
    ]
  }
}
`)
	if err := os.WriteFile(codexPath, existing, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "codex",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "UserPromptSubmit") {
		t.Errorf("stdout = %q; want mention of UserPromptSubmit in Added summary", output)
	}
	if !strings.Contains(output, "Added") && !strings.Contains(output, "追加") {
		t.Errorf("stdout = %q; want Added/追加 summary line", output)
	}

	content, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "UserPromptSubmit") {
		t.Errorf("codex hooks.json = %q; want UserPromptSubmit after --upgrade", content)
	}
}

// TestHooksInstall_UpgradeIsIdempotent asserts that re-running
// `--upgrade` on an already up-to-date config is a no-op that reports
// zero added / refreshed events and leaves the file byte-identical.
func TestHooksInstall_UpgradeIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// First run: write full hook config.
	rootCmd := newTestRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "codex",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}

	codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
	firstContent, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// Second run: expect no-op summary.
	rootCmd = newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "codex",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "up to date") && !strings.Contains(output, "最新") {
		t.Errorf("stdout = %q; want up-to-date notice on idempotent re-run", output)
	}

	secondContent, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(firstContent) != string(secondContent) {
		t.Fatalf("idempotent re-run changed file bytes\nfirst:\n%s\nsecond:\n%s", firstContent, secondContent)
	}
}

// TestHooksInstall_UpgradeRejectsForceCombination ensures --upgrade and
// --force produce a clear validation error instead of silently mixing
// semantics.
func TestHooksInstall_UpgradeRejectsForceCombination(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "codex",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
		"--force",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil; want mutual-exclusion error")
	}
	if !strings.Contains(err.Error(), "--upgrade") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v; want mention of --upgrade and --force", err)
	}
}

// TestHooksInstall_UpgradeSkipsWhenClaudePluginActive asserts that
// `--upgrade` on the Claude client short-circuits when the Traceary
// Claude Code plugin is enabled, and that the skip notice does NOT
// suggest `--force` as the remediation (that flag would overwrite user
// hooks, which is the opposite of what --upgrade wants).
func TestHooksInstall_UpgradeSkipsWhenClaudePluginActive(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	// Write a plugin-active settings.json in the fake home directory.
	if err := os.MkdirAll(filepath.Join(homeDir, ".claude"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".claude", "settings.json"), []byte(`{
  "enabledPlugins": {
    "traceary@traceary-plugins": true
  }
}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
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
		"--upgrade",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("settings.json should not be written under --upgrade when plugin is active; stat err = %v", err)
	}

	output := stdout.String()
	// The upgrade-specific skip notice must name the plugin and NOT
	// point users at --force (which would clobber user hooks).
	if !strings.Contains(output, "traceary@traceary-plugins") {
		t.Errorf("stdout = %q; want upgrade-skip notice naming the plugin", output)
	}
	if strings.Contains(output, "--force") {
		t.Errorf("stdout = %q; --upgrade skip notice must NOT suggest --force (would clobber user hooks)", output)
	}
}

// TestHooksInstall_UpgradeDetectsMatcherPresetChange asserts that a
// Claude --matcher preset change (minimal → all) is surfaced as a
// Refreshed event instead of being misreported as "already up to date"
// while the file bytes still change. Regression guard for the Codex
// verifier finding on PR #656.
func TestHooksInstall_UpgradeDetectsMatcherPresetChange(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// First upgrade with --matcher minimal so the file carries the
	// minimal matcher row set.
	rootCmd := newTestRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "claude",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
		"--matcher", "minimal",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
	minimalContent, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// Second upgrade with --matcher all changes Claude matcher rows
	// even though the managed command keys remain identical. The diff
	// must classify affected events as Refreshed, not Preserved.
	rootCmd = newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "claude",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
		"--matcher", "all",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}

	allContent, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(minimalContent) == string(allContent) {
		t.Fatalf("matcher change minimal→all should rewrite file; contents identical")
	}

	output := stdout.String()
	if strings.Contains(output, "already up to date") || strings.Contains(output, "既に最新") {
		t.Errorf("stdout = %q; matcher-only change must NOT report 'already up to date'", output)
	}
	if !strings.Contains(output, "Refreshed") && !strings.Contains(output, "更新") {
		t.Errorf("stdout = %q; matcher-only change must surface a Refreshed summary", output)
	}
}

// TestHooksInstall_UpgradePreservesUserAddedHooks asserts that user
// hooks in the target file are left alone while only Traceary-managed
// entries are refreshed.
func TestHooksInstall_UpgradePreservesUserAddedHooks(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	settingsPath := filepath.Join(projectDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existing := []byte(`{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "echo user-hook"}]}
    ]
  }
}
`)
	if err := os.WriteFile(settingsPath, existing, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rootCmd := newTestRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "gemini",
		"--project-dir", projectDir,
		"--traceary-bin", "traceary",
		"--upgrade",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), `"theme": "dark"`) {
		t.Errorf("merged settings lost unrelated top-level field: %s", content)
	}
	if !strings.Contains(string(content), `"command": "echo user-hook"`) {
		t.Errorf("merged settings lost user-added hook: %s", content)
	}
}

// TestHooksInstall_UpgradeWithNonCanonicalTracearyBin asserts that
// --upgrade correctly reports Added events when the Traceary binary is
// installed under a non-canonical basename (e.g. a dev build at
// /tmp/traceary-qa). Regression guard for #667: before the fix,
// `tracearyManagedKeySet` rejected the existing entries because
// `filepath.Base("/tmp/traceary-qa") != "traceary"`, which made the
// upgrade silently modify the file while reporting "0 event(s)
// unchanged" to stdout.
func TestHooksInstall_UpgradeWithNonCanonicalTracearyBin(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// Seed a Gemini settings.json with pre-v0.8 Traceary-managed
	// entries but no AfterAgent, all referencing a non-canonical
	// binary basename. The entry names still carry the canonical
	// `traceary-*` prefix, which is the signal `--upgrade` uses to
	// recognize them as managed.
	geminiPath := filepath.Join(homeDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(geminiPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	existing := []byte(`{
  "hooks": {
    "SessionStart": [{"matcher":"*","hooks":[{"name":"traceary-session-start","type":"command","command":"'/tmp/traceary-qa' 'hook' 'session' 'gemini' 'start'","timeout":5000,"description":"Start a Traceary session"}]}],
    "SessionEnd":   [{"matcher":"*","hooks":[{"name":"traceary-session-end","type":"command","command":"'/tmp/traceary-qa' 'hook' 'session' 'gemini' 'end'","timeout":5000,"description":"Finish a Traceary session"}]}],
    "AfterTool":    [{"matcher":"run_shell_command","hooks":[{"name":"traceary-audit","type":"command","command":"'/tmp/traceary-qa' 'hook' 'audit' 'gemini'","timeout":5000,"description":"Record shell command audits in Traceary"}]}]
  }
}
`)
	if err := os.WriteFile(geminiPath, existing, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks", "install",
		"--client", "gemini",
		"--project-dir", projectDir,
		"--global",
		"--traceary-bin", "/tmp/traceary-qa",
		"--upgrade",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	// Summary must acknowledge AfterAgent was added, not misreport 0 events.
	if !strings.Contains(output, "AfterAgent") {
		t.Errorf("stdout = %q; want AfterAgent to appear in the summary", output)
	}
	if strings.Contains(output, "0 event(s) unchanged") || strings.Contains(output, "already up to date") {
		t.Errorf("stdout = %q; must not misreport no-op when AfterAgent was actually added", output)
	}

	// File must have AfterAgent wired.
	content, err := os.ReadFile(geminiPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "AfterAgent") {
		t.Errorf("gemini settings.json = %q; want AfterAgent added after --upgrade", content)
	}
}
