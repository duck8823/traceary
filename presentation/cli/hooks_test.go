package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/presentation/cli"
)

type printedHooksSettings struct {
	Hooks map[string][]printedHookMatcher `json:"hooks"`
}

type printedHookMatcher struct {
	Matcher *string              `json:"matcher,omitempty"`
	Hooks   []printedHookCommand `json:"hooks"`
}

type printedHookCommand struct {
	Name        string `json:"name,omitempty"`
	Type        string `json:"type"`
	Command     string `json:"command"`
	Timeout     int    `json:"timeout,omitempty"`
	Description string `json:"description,omitempty"`
}

func TestRootCLI_HooksPrintCommand(t *testing.T) {
	tracearyBin := filepath.Join("/tmp", "traceary bin", "traceary")

	t.Run("prints Claude hook settings", func(t *testing.T) {
		settings := executeHooksPrint(t, "claude", tracearyBin)
		if diff := cmp.Diff("*", *settings.Hooks["SessionStart"][0].Matcher); diff != "" {
			t.Fatalf("SessionStart matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'session' 'claude' 'start'`, settings.Hooks["SessionStart"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("compact", *settings.Hooks["SessionStart"][1].Matcher); diff != "" {
			t.Fatalf("SessionStart[1] matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'compact' 'claude' 'session-start-compact'`, settings.Hooks["SessionStart"][1].Hooks[0].Command); diff != "" {
			t.Fatalf("SessionStart[1] command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("mcp__.*", *settings.Hooks["PostToolUse"][1].Matcher); diff != "" {
			t.Fatalf("PostToolUse[1] matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'audit' 'claude'`, settings.Hooks["PostToolUseFailure"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("PostToolUseFailure command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("mcp__.*", *settings.Hooks["PostToolUseFailure"][1].Matcher); diff != "" {
			t.Fatalf("PostToolUseFailure[1] matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("*", *settings.Hooks["PostCompact"][0].Matcher); diff != "" {
			t.Fatalf("PostCompact matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'compact' 'claude' 'post-compact'`, settings.Hooks["PostCompact"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("PostCompact command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("*", *settings.Hooks["UserPromptSubmit"][0].Matcher); diff != "" {
			t.Fatalf("UserPromptSubmit matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'prompt' 'claude'`, settings.Hooks["UserPromptSubmit"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("UserPromptSubmit command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("prints settings with Claude Code alias", func(t *testing.T) {
		settings := executeHooksPrint(t, "claude-code", tracearyBin)
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'session' 'claude' 'start'`, settings.Hooks["SessionStart"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("prints Codex hook settings", func(t *testing.T) {
		settings := executeHooksPrint(t, "codex", tracearyBin)
		if diff := cmp.Diff((*string)(nil), settings.Hooks["SessionStart"][0].Matcher); diff != "" {
			t.Fatalf("SessionStart matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff((*string)(nil), settings.Hooks["UserPromptSubmit"][0].Matcher); diff != "" {
			t.Fatalf("UserPromptSubmit matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'prompt' 'codex'`, settings.Hooks["UserPromptSubmit"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("UserPromptSubmit command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("", *settings.Hooks["PostToolUse"][0].Matcher); diff != "" {
			t.Fatalf("PostToolUse matcher mismatch (-want +got):\n%s", diff)
		}
		// Transcript runs before session-stop in the Stop entry:
		// session-stop clears the cached session / workspace state
		// files as part of teardown, so running it first would make
		// the transcript hook silent-skip if the payload ever omits
		// `session_id` (see codex_hooks_handler.go).
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'transcript' 'codex'`, settings.Hooks["Stop"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("Stop[0] command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'session' 'codex' 'stop'`, settings.Hooks["Stop"][0].Hooks[1].Command); diff != "" {
			t.Fatalf("Stop[1] command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("prints Gemini hook settings", func(t *testing.T) {
		settings := executeHooksPrint(t, "gemini", tracearyBin)
		if diff := cmp.Diff("run_shell_command", *settings.Hooks["AfterTool"][0].Matcher); diff != "" {
			t.Fatalf("AfterTool matcher mismatch (-want +got):\n%s", diff)
		}
		gotHook := settings.Hooks["SessionStart"][0].Hooks[0]
		if diff := cmp.Diff("traceary-session-start", gotHook.Name); diff != "" {
			t.Fatalf("SessionStart hook name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(10000, gotHook.Timeout); diff != "" {
			t.Fatalf("SessionStart hook timeout mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("*", *settings.Hooks["AfterAgent"][0].Matcher); diff != "" {
			t.Fatalf("AfterAgent matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'/tmp/traceary bin/traceary' 'hook' 'transcript' 'gemini'`, settings.Hooks["AfterAgent"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("AfterAgent command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("prints Antigravity group document", func(t *testing.T) {
		// Antigravity uses a top-level hook-group map (Traceary owns the
		// "traceary" group), not the shared {"hooks": {...}} envelope, so the
		// printed document is parsed with a dedicated shape.
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print", "--client", "antigravity", "--traceary-bin", tracearyBin})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		var doc map[string]map[string]json.RawMessage
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
			t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.Bytes())
		}
		group, ok := doc["traceary"]
		if !ok {
			t.Fatalf("printed Antigravity document missing the traceary group:\n%s", stdout.Bytes())
		}
		for _, event := range []string{"PreInvocation", "PreToolUse", "PostToolUse", "Stop"} {
			if _, ok := group[event]; !ok {
				t.Fatalf("printed Antigravity group missing %s event:\n%s", event, stdout.Bytes())
			}
		}
		if !strings.Contains(stdout.String(), `'/tmp/traceary bin/traceary' 'hook' 'antigravity' 'pre-invocation'`) {
			t.Fatalf("printed Antigravity document missing the PreInvocation runtime command:\n%s", stdout.Bytes())
		}
	})

	t.Run("prints Antigravity group document with agy alias", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print", "--client", "agy", "--traceary-bin", tracearyBin})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
			t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.Bytes())
		}
		if _, ok := doc["traceary"]; !ok {
			t.Fatalf("agy alias did not print the traceary group:\n%s", stdout.Bytes())
		}
	})

	for _, client := range []string{"grok", "grok-build", "grok-cli"} {
		t.Run("reaches empty Grok boundary with "+client, func(t *testing.T) {
			rootCmd := newTestRootCLI().Command()
			stdout := &bytes.Buffer{}
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"hooks", "print", "--client", client, "--traceary-bin", tracearyBin})
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			var settings printedHooksSettings
			if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
				t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.Bytes())
			}
			if len(settings.Hooks) != 0 {
				t.Fatalf("Grok hooks = %v, want empty boundary before runtime support", settings.Hooks)
			}
		})
	}

	t.Run("uses stable command name when traceary-bin is not specified", func(t *testing.T) {
		settings := executeHooksPrintWithoutTracearyBin(t, "claude")
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'claude' 'start'`, settings.Hooks["SessionStart"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("unsupported client returns an English error by default", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"print",
			"--client", "unknown",
			"--traceary-bin", tracearyBin,
		})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "valid values: claude, codex, gemini") {
			t.Fatalf("error = %q, want valid values", err.Error())
		}
	})

	t.Run("missing client returns a discoverable error", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print"})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "supported: claude, codex, gemini") {
			t.Fatalf("error = %q, want supported client list", err.Error())
		}
	})
}

func TestRootCLI_HooksInstallCommand(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("fails closed for Grok until native runtime support lands", func(t *testing.T) {
		for _, client := range []string{"grok", "grok-build", "grok-cli"} {
			for _, tc := range []struct {
				name       string
				extraArgs  []string
				seedOutput bool
			}{
				{name: "default path"},
				{name: "explicit output", extraArgs: []string{"--output", filepath.Join(t.TempDir(), "hooks.json")}},
				{name: "force with explicit output", extraArgs: []string{"--output", filepath.Join(t.TempDir(), "hooks.json"), "--force"}, seedOutput: true},
				{name: "upgrade with explicit output", extraArgs: []string{"--output", filepath.Join(t.TempDir(), "hooks.json"), "--upgrade"}, seedOutput: true},
			} {
				t.Run(client+"/"+tc.name, func(t *testing.T) {
					args := []string{
						"hooks", "install",
						"--client", client,
						"--project-dir", projectDir,
						"--traceary-bin", "traceary",
					}
					args = append(args, tc.extraArgs...)
					var outputPath string
					for i, arg := range args {
						if arg == "--output" {
							outputPath = args[i+1]
						}
					}
					const original = `{"user":"content"}`
					if tc.seedOutput {
						if err := os.WriteFile(outputPath, []byte(original), 0o600); err != nil {
							t.Fatalf("seed output: %v", err)
						}
					}

					rootCmd := newTestRootCLI().Command()
					rootCmd.SetOut(&bytes.Buffer{})
					rootCmd.SetErr(&bytes.Buffer{})
					rootCmd.SetArgs(args)
					err := rootCmd.Execute()
					if err == nil || !strings.Contains(err.Error(), "native runtime support") {
						t.Fatalf("Execute() error = %v, want fail-closed native runtime support error", err)
					}
					if outputPath == "" {
						return
					}
					content, readErr := os.ReadFile(outputPath)
					if tc.seedOutput {
						if readErr != nil || string(content) != original {
							t.Fatalf("output after failed install = %q, %v; want unchanged", content, readErr)
						}
					} else if !os.IsNotExist(readErr) {
						t.Fatalf("output created after failed install: content=%q, error=%v", content, readErr)
					}
				})
			}
		}
	})

	t.Run("installs Claude settings to standard path", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"install",
			"--client", "claude",
			"--project-dir", projectDir,
			"--traceary-bin", "traceary",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		outputPath := filepath.Join(projectDir, ".claude", "settings.json")
		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}

		var settings printedHooksSettings
		if err := json.Unmarshal(content, &settings); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if _, ok := settings.Hooks["SessionStart"]; !ok {
			t.Fatalf("SessionStart hook not found")
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'claude' 'start'`, settings.Hooks["SessionStart"][0].Hooks[0].Command); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
		if !strings.Contains(stdout.String(), outputPath) {
			t.Fatalf("stdout = %q, want path %q", stdout.String(), outputPath)
		}
		if !strings.Contains(stdout.String(), "traceary doctor --client claude") {
			t.Fatalf("stdout = %q, want doctor hint", stdout.String())
		}
	})

	t.Run("installs settings to standard path with Codex CLI alias", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"install",
			"--client", "codex-cli",
			"--project-dir", projectDir,
			"--traceary-bin", "traceary",
			"--force",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		outputPath := filepath.Join(homeDir, ".codex", "hooks.json")
		if _, err := os.Stat(outputPath); err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
	})

	t.Run("installs Codex settings to home directory standard path", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"install",
			"--client", "codex",
			"--project-dir", projectDir,
			"--traceary-bin", "traceary",
			"--force",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		outputPath := filepath.Join(homeDir, ".codex", "hooks.json")
		if _, err := os.Stat(outputPath); err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
	})

	t.Run("existing file without force merges supported JSON settings", func(t *testing.T) {
		outputPath := filepath.Join(projectDir, ".gemini", "settings.json")
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(outputPath, []byte(`{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "custom-session-start",
            "type": "command",
            "command": "echo custom-start"
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
`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"install",
			"--client", "gemini",
			"--project-dir", projectDir,
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if !strings.Contains(string(content), `"theme": "dark"`) {
			t.Fatalf("merged settings lost unrelated top-level field: %s", content)
		}
		if !strings.Contains(string(content), `"command": "echo custom-start"`) {
			t.Fatalf("merged settings lost custom hook: %s", content)
		}
		if strings.Count(string(content), "traceary-session-start") != 1 {
			t.Fatalf("merged settings should contain exactly one traceary session start hook: %s", content)
		}
	})

	t.Run("overwrites existing file with force flag", func(t *testing.T) {
		outputPath := filepath.Join(projectDir, ".gemini", "settings.json")
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(outputPath, []byte("{\"existing\":true}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"install",
			"--client", "gemini",
			"--project-dir", projectDir,
			"--force",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if strings.Contains(string(content), "\"existing\":true") {
			t.Fatalf("settings.json was not overwritten")
		}
	})

	t.Run("invalid existing JSON fails with a merge error", func(t *testing.T) {
		outputPath := filepath.Join(projectDir, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(outputPath, []byte("{invalid json"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"install",
			"--client", "claude",
			"--project-dir", projectDir,
			"--traceary-bin", "traceary",
		})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "failed to merge existing hook configuration") {
			t.Fatalf("error = %q, want merge error", err.Error())
		}
	})
}

func TestRootCLI_HooksPrintCommand_MatcherPreset(t *testing.T) {
	tracearyBin := "traceary"

	t.Run("claude + matcher=minimal drops built-in row", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print", "--client", "claude", "--traceary-bin", tracearyBin, "--matcher", "minimal"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		var settings printedHooksSettings
		if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		postToolUse := settings.Hooks["PostToolUse"]
		if got, want := len(postToolUse), 2; got != want {
			t.Fatalf("PostToolUse len = %d, want %d", got, want)
		}
	})

	t.Run("claude + matcher=all appends .*", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print", "--client", "claude", "--traceary-bin", tracearyBin, "--matcher", "all"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		var settings printedHooksSettings
		if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		postToolUse := settings.Hooks["PostToolUse"]
		if got, want := len(postToolUse), 3; got != want {
			t.Fatalf("PostToolUse len = %d, want %d", got, want)
		}
		third := postToolUse[2].Matcher
		if third == nil || *third != ".*" {
			t.Fatalf("PostToolUse[2].Matcher = %v, want .*", third)
		}
	})

	t.Run("invalid matcher value returns an error", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print", "--client", "claude", "--traceary-bin", tracearyBin, "--matcher", "lol"})
		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want validation error")
		}
		if !strings.Contains(err.Error(), "--matcher") {
			t.Errorf("Execute() error = %v, want message containing --matcher", err)
		}
	})

	t.Run("non-claude client ignores matcher (no validation bypass)", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "print", "--client", "codex", "--traceary-bin", tracearyBin, "--matcher", "minimal"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// Codex settings should still render without panic; the matcher is ignored.
		if stdout.Len() == 0 {
			t.Fatalf("expected non-empty output for codex")
		}
	})
}

func executeHooksPrint(
	t *testing.T,
	client string,
	tracearyBin string,
) *printedHooksSettings {
	t.Helper()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks",
		"print",
		"--client", client,
		"--traceary-bin", tracearyBin,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var settings printedHooksSettings
	if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return &settings
}

func executeHooksPrintWithoutTracearyBin(
	t *testing.T,
	client string,
) *printedHooksSettings {
	t.Helper()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks",
		"print",
		"--client", client,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var settings printedHooksSettings
	if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return &settings
}
