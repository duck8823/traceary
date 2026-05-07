package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestRemovedTopLevelAliasesReportReplacement confirms that the v0.9-era
// top-level aliases (init / gc / backup / handoff / compact-summary) no
// longer execute and instead surface a usage error pointing at the
// canonical replacement command. The aliases were retired in v0.14.0
// (#918); this test guards against accidental re-registration and
// against the replacement hint going stale.
func TestRemovedTopLevelAliasesReportReplacement(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		args        []string
		alias       string
		replacement string
	}{
		{name: "init", args: []string{"init"}, alias: "init", replacement: "traceary store init"},
		{name: "gc", args: []string{"gc"}, alias: "gc", replacement: "traceary store gc"},
		{name: "backup", args: []string{"backup"}, alias: "backup", replacement: "traceary store backup"},
		{name: "backup create still hits the stub", args: []string{"backup", "create"}, alias: "backup", replacement: "traceary store backup"},
		{name: "handoff", args: []string{"handoff"}, alias: "handoff", replacement: "traceary session handoff"},
		{name: "compact-summary", args: []string{"compact-summary"}, alias: "compact-summary", replacement: "traceary session handoff --compact-only"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(stderr)
			rootCmd.SetArgs(tc.args)

			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want removed-alias error", tc.args)
			}
			msg := err.Error()
			if !strings.Contains(msg, "removed in v0.14.0") {
				t.Fatalf("error %q missing removal version", msg)
			}
			if !strings.Contains(msg, tc.alias) {
				t.Fatalf("error %q missing legacy alias name %q", msg, tc.alias)
			}
			if !strings.Contains(msg, tc.replacement) {
				t.Fatalf("error %q missing replacement %q", msg, tc.replacement)
			}
		})
	}
}

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
