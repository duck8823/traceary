package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		if diff := cmp.Diff("go test ./...", stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
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
		if diff := cmp.Diff(`{"error":"boom","is_interrupt":false}`, stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
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
		if diff := cmp.Diff("github.com/duck8823/traceary", stdout.String()); diff != "" {
			t.Fatalf("stdout mismatch (-want +got):\n%s", diff)
		}
	})
}
