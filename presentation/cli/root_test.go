package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCLI_Command_SilencesCobraErrorOutput(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI().Command()
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
	rootCmd := NewRootCLI().Command()
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
	rootCmd := NewRootCLI().Command()
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

func TestRootCLI_AuditHelpMentionsDefaults(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"audit", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "TRACEARY_SESSION_ID") {
		t.Fatalf("stdout = %q, want TRACEARY_SESSION_ID", stdout.String())
	}
	if !strings.Contains(stdout.String(), "TRACEARY_ALLOW_SECRETS") {
		t.Fatalf("stdout = %q, want TRACEARY_ALLOW_SECRETS", stdout.String())
	}
}

func TestRootCLI_SessionLatestHelpExplainsSemantics(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"session", "latest", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "most recent lifecycle boundary") {
		t.Fatalf("stdout = %q, want lifecycle boundary explanation", stdout.String())
	}
}
