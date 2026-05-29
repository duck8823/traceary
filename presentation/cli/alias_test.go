package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestRemovedTopLevelAliasesAreHiddenFromHelp confirms the retired
// aliases no longer appear in `traceary --help` output.
func TestRemovedTopLevelAliasesAreHiddenFromHelp(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(--help) error = %v", err)
	}

	help := stdout.String()
	available := extractAvailableCommandsBlock(help)
	for _, removed := range []string{"compact-summary", "handoff", "  init", "  backup", "  gc"} {
		if strings.Contains(available, removed) {
			t.Fatalf("traceary --help still advertises removed alias %q:\n%s", removed, available)
		}
	}
}

// extractAvailableCommandsBlock returns the "Available Commands"
// section of a Cobra help output. Cobra renders the section as a list
// of two-space-indented "  name   short" entries between the
// "Available Commands:" header and the next blank line.
func extractAvailableCommandsBlock(help string) string {
	const header = "Available Commands:"
	start := strings.Index(help, header)
	if start < 0 {
		return help
	}
	rest := help[start+len(header):]
	end := strings.Index(rest, "\n\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
