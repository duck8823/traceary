package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
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

		rootCmd := cli.NewRootCLI(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Command()
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"hooks",
			"print",
			"--client", "unknown",
			"--project-dir", projectDir,
			"--traceary-bin", tracearyBin,
		})

		if err := rootCmd.Execute(); err == nil {
			t.Fatalf("Execute() error = nil, want error")
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

	rootCmd := cli.NewRootCLI(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Command()
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

	rootCmd := cli.NewRootCLI(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Command()
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
