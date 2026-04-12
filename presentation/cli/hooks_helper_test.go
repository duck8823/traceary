package cli_test

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCLI_HooksHelperCommand(t *testing.T) {
	t.Run("json-get reads nested values", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(`{"tool_input":{"command":"go test ./..."}}`))
		rootCmd.SetArgs([]string{"hooks", "helper", "json-get", "tool_input.command"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got, want := stdout.String(), "go test ./..."; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("build-failure-output renders compact JSON", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetIn(strings.NewReader(`{"error":"boom","is_interrupt":false}`))
		rootCmd.SetArgs([]string{"hooks", "helper", "build-failure-output"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got, want := stdout.String(), `{"error":"boom","is_interrupt":false}`; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("normalize-git-remote normalizes git URLs", func(t *testing.T) {
		rootCmd := newTestRootCLI().Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hooks", "helper", "normalize-git-remote", "git@github.com:duck8823/traceary.git"})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got, want := stdout.String(), "github.com/duck8823/traceary"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}
