package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HooksGuideCommand(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"hooks",
		"guide",
		"--client", "gemini",
		"--project-dir", projectDir,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "traceary hooks install --client gemini") {
		t.Fatalf("stdout = %q, want install step", output)
	}
	if !strings.Contains(output, "traceary doctor --client gemini") {
		t.Fatalf("stdout = %q, want doctor step", output)
	}
	if !strings.Contains(output, "hooksConfig.enabled=true") {
		t.Fatalf("stdout = %q, want Gemini note", output)
	}
}

func TestRootCLI_HooksGuideCommand_Antigravity(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	// The agy alias must normalize to the canonical antigravity client.
	rootCmd.SetArgs([]string{
		"hooks",
		"guide",
		"--client", "agy",
		"--project-dir", projectDir,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "traceary hooks install --client antigravity") {
		t.Fatalf("stdout = %q, want install step normalized to antigravity", output)
	}
	if !strings.Contains(output, ".agents/hooks.json") {
		t.Fatalf("stdout = %q, want the workspace .agents/hooks.json path", output)
	}
	if !strings.Contains(output, "PreInvocation") {
		t.Fatalf("stdout = %q, want the Antigravity PreInvocation note", output)
	}
}

func TestRootCLI_HooksGuideCommand_MissingClientReturnsError(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"hooks", "guide"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if diff := cmp.Diff(`required flag(s) "client" not set`, err.Error()); diff != "" {
		t.Fatalf("Execute() error mismatch (-want +got):\n%s", diff)
	}
}
