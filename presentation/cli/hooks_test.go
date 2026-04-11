package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	scriptsDir := filepath.Join(t.TempDir(), "hook scripts")
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", scriptsDir)

	t.Run("Claude 向け設定を出力できる", func(t *testing.T) {
		settings := executeHooksPrint(t, "claude", tracearyBin)
		if got, want := *settings.Hooks["SessionStart"][0].Matcher, "*"; got != want {
			t.Fatalf("SessionStart matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-session.sh")+`' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
		if got, want := *settings.Hooks["SessionStart"][1].Matcher, "compact"; got != want {
			t.Fatalf("SessionStart[1] matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["SessionStart"][1].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-compact.sh")+`' 'claude' 'session-start-compact'`; got != want {
			t.Fatalf("SessionStart[1] command = %q, want %q", got, want)
		}
		if got, want := *settings.Hooks["PostToolUse"][1].Matcher, "mcp__.*"; got != want {
			t.Fatalf("PostToolUse[1] matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["PostToolUseFailure"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-audit.sh")+`' 'claude'`; got != want {
			t.Fatalf("PostToolUseFailure command = %q, want %q", got, want)
		}
		if got, want := *settings.Hooks["PostToolUseFailure"][1].Matcher, "mcp__.*"; got != want {
			t.Fatalf("PostToolUseFailure[1] matcher = %q, want %q", got, want)
		}
		if got, want := *settings.Hooks["PostCompact"][0].Matcher, "*"; got != want {
			t.Fatalf("PostCompact matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["PostCompact"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-compact.sh")+`' 'claude' 'post-compact'`; got != want {
			t.Fatalf("PostCompact command = %q, want %q", got, want)
		}
		if got, want := *settings.Hooks["UserPromptSubmit"][0].Matcher, "*"; got != want {
			t.Fatalf("UserPromptSubmit matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["UserPromptSubmit"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-prompt.sh")+`' 'claude'`; got != want {
			t.Fatalf("UserPromptSubmit command = %q, want %q", got, want)
		}
		assertInstalledHookScripts(t, scriptsDir)
	})

	t.Run("Claude Code alias でも設定を出力できる", func(t *testing.T) {
		settings := executeHooksPrint(t, "claude-code", tracearyBin)
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-session.sh")+`' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
	})

	t.Run("Codex 向け設定を出力できる", func(t *testing.T) {
		settings := executeHooksPrint(t, "codex", tracearyBin)
		if settings.Hooks["SessionStart"][0].Matcher != nil {
			t.Fatalf("SessionStart matcher = %v, want nil", settings.Hooks["SessionStart"][0].Matcher)
		}
		if got, want := *settings.Hooks["PostToolUse"][0].Matcher, ""; got != want {
			t.Fatalf("PostToolUse matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["Stop"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '`+filepath.Join(scriptsDir, "traceary-session.sh")+`' 'codex' 'stop'`; got != want {
			t.Fatalf("Stop command = %q, want %q", got, want)
		}
	})

	t.Run("Gemini 向け設定を出力できる", func(t *testing.T) {
		settings := executeHooksPrint(t, "gemini", tracearyBin)
		if got, want := *settings.Hooks["AfterTool"][0].Matcher, "run_shell_command"; got != want {
			t.Fatalf("AfterTool matcher = %q, want %q", got, want)
		}
		gotHook := settings.Hooks["SessionStart"][0].Hooks[0]
		if got, want := gotHook.Name, "traceary-session-start"; got != want {
			t.Fatalf("SessionStart hook name = %q, want %q", got, want)
		}
		if got, want := gotHook.Timeout, 5000; got != want {
			t.Fatalf("SessionStart hook timeout = %d, want %d", got, want)
		}
	})

	t.Run("traceary-bin 未指定時は stable command 名を使う", func(t *testing.T) {
		settings := executeHooksPrintWithoutTracearyBin(t, "claude")
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='traceary' bash '`+filepath.Join(scriptsDir, "traceary-session.sh")+`' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
	})

	t.Run("unsupported client returns an English error by default", func(t *testing.T) {
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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
	scriptsDir := filepath.Join(t.TempDir(), "hook scripts")
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", scriptsDir)
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("Claude 向け設定を標準パスへ書き出せる", func(t *testing.T) {
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='traceary' bash '`+filepath.Join(scriptsDir, "traceary-session.sh")+`' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
		if !strings.Contains(stdout.String(), outputPath) {
			t.Fatalf("stdout = %q, want path %q", stdout.String(), outputPath)
		}
		if !strings.Contains(stdout.String(), "traceary doctor --client claude") {
			t.Fatalf("stdout = %q, want doctor hint", stdout.String())
		}
		assertInstalledHookScripts(t, scriptsDir)
	})

	t.Run("Codex CLI alias でも標準パスへ書き出せる", func(t *testing.T) {
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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

	t.Run("Codex 向け設定はホーム配下の標準パスへ書き出す", func(t *testing.T) {
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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

	t.Run("force を付けると既存ファイルを上書きできる", func(t *testing.T) {
		outputPath := filepath.Join(projectDir, ".gemini", "settings.json")
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(outputPath, []byte("{\"existing\":true}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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

func assertInstalledHookScripts(t *testing.T, scriptsDir string) {
	t.Helper()

	for _, scriptName := range []string{"common.sh", "traceary-session.sh", "traceary-audit.sh", "traceary-compact.sh", "traceary-prompt.sh"} {
		scriptPath := filepath.Join(scriptsDir, scriptName)
		info, err := os.Stat(scriptPath)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", scriptPath, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%q is not executable: mode=%#o", scriptPath, info.Mode().Perm())
		}
	}
}

func executeHooksPrint(
	t *testing.T,
	client string,
	tracearyBin string,
) *printedHooksSettings {
	t.Helper()

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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
