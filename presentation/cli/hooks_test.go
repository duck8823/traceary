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
	t.Parallel()

	projectDir := filepath.Join("/tmp", "traceary repo")
	tracearyBin := filepath.Join("/tmp", "traceary bin", "traceary")

	t.Run("Claude 向け設定を出力できる", func(t *testing.T) {
		t.Parallel()

		settings := executeHooksPrint(t, "claude", projectDir, tracearyBin)
		if got, want := *settings.Hooks["SessionStart"][0].Matcher, "*"; got != want {
			t.Fatalf("SessionStart matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '/tmp/traceary repo/scripts/hooks/traceary-session.sh' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["PostToolUseFailure"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '/tmp/traceary repo/scripts/hooks/traceary-audit.sh' 'claude'`; got != want {
			t.Fatalf("PostToolUseFailure command = %q, want %q", got, want)
		}
	})

	t.Run("Claude Code alias でも設定を出力できる", func(t *testing.T) {
		t.Parallel()

		settings := executeHooksPrint(t, "claude-code", projectDir, tracearyBin)
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '/tmp/traceary repo/scripts/hooks/traceary-session.sh' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
	})

	t.Run("Codex 向け設定を出力できる", func(t *testing.T) {
		t.Parallel()

		settings := executeHooksPrint(t, "codex", projectDir, tracearyBin)
		if settings.Hooks["SessionStart"][0].Matcher != nil {
			t.Fatalf("SessionStart matcher = %v, want nil", settings.Hooks["SessionStart"][0].Matcher)
		}
		if got, want := *settings.Hooks["PostToolUse"][0].Matcher, ""; got != want {
			t.Fatalf("PostToolUse matcher = %q, want %q", got, want)
		}
		if got, want := settings.Hooks["Stop"][0].Hooks[0].Command,
			`TRACEARY_BIN='/tmp/traceary bin/traceary' bash '/tmp/traceary repo/scripts/hooks/traceary-session.sh' 'codex' 'stop'`; got != want {
			t.Fatalf("Stop command = %q, want %q", got, want)
		}
	})

	t.Run("Gemini 向け設定を出力できる", func(t *testing.T) {
		t.Parallel()

		settings := executeHooksPrint(t, "gemini", projectDir, tracearyBin)
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
		t.Parallel()

		settings := executeHooksPrintWithoutTracearyBin(t, "claude", projectDir)
		if got, want := settings.Hooks["SessionStart"][0].Hooks[0].Command,
			`TRACEARY_BIN='traceary' bash '/tmp/traceary repo/scripts/hooks/traceary-session.sh' 'claude' 'start'`; got != want {
			t.Fatalf("SessionStart command = %q, want %q", got, want)
		}
	})

	t.Run("未対応 client はエラー", func(t *testing.T) {
		t.Parallel()

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"print",
			"--client", "unknown",
			"--project-dir", projectDir,
			"--traceary-bin", tracearyBin,
		})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "有効値: claude, codex, gemini") {
			t.Fatalf("error = %q, want valid values", err.Error())
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
		if !strings.Contains(stdout.String(), outputPath) {
			t.Fatalf("stdout = %q, want path %q", stdout.String(), outputPath)
		}
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

	t.Run("既存ファイルがあるとき force なしでは失敗する", func(t *testing.T) {
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
		})

		err := rootCmd.Execute()
		if err == nil {
			t.Fatalf("Execute() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "既存ファイルがあるため上書きしません") {
			t.Fatalf("error = %q, want overwrite warning", err.Error())
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
}

func executeHooksPrint(
	t *testing.T,
	client string,
	projectDir string,
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
		"--project-dir", projectDir,
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
	projectDir string,
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
		"--project-dir", projectDir,
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
