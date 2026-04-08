package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCLI_Command_SilencesCobraErrorOutput(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI(RootCLIOptions{}).Command()
	if !rootCmd.SilenceErrors {
		t.Fatal("rootCmd.SilenceErrors = false, want true")
	}
	if !rootCmd.SilenceUsage {
		t.Fatal("rootCmd.SilenceUsage = false, want true")
	}
}

func TestRootCLI_HelpDefaultsToEnglish(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI(RootCLIOptions{}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Local-first CLI for AI agent work history") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRootCLI_HelpCanUseJapanese(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "ja")

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI(RootCLIOptions{}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "AI エージェントの作業履歴をローカルに記録する CLI") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
