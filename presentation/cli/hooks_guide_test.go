package cli_test

import (
	"bytes"
	"strings"
	"testing"

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

	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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
	if !strings.Contains(stdout.String(), "traceary hooks install --client gemini") {
		t.Fatalf("stdout = %q, want install step", stdout.String())
	}
	if !strings.Contains(stdout.String(), "traceary doctor --client gemini") {
		t.Fatalf("stdout = %q, want doctor step", stdout.String())
	}
	if !strings.Contains(stdout.String(), "hooksConfig.enabled=true") {
		t.Fatalf("stdout = %q, want Gemini note", stdout.String())
	}
}
