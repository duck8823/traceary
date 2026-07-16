package cli_test

import (
	"bytes"
	"strings"
	"testing"
)

// TestRootCLI_IntegrationSubtreeRemoved confirms the entire
// `traceary integration` command tree was deleted in v0.25.0 (#1266).
// Legacy invocations must fail as unknown commands (no migration stubs).
func TestRootCLI_IntegrationSubtreeRemoved(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"integration"},
		{"integration", "codex", "install"},
		{"integration", "codex", "uninstall"},
	} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			rootCmd := newTestRootCLI().Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(args)
			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want unknown-command error after v0.25.0 removal", args)
			}
			msg := strings.ToLower(err.Error())
			// Cobra unknown command / unknown subcommand wording.
			if !strings.Contains(msg, "unknown") && !strings.Contains(msg, "invalid") && !strings.Contains(msg, "not found") {
				// Also accept Japanese localization of unknown errors.
				if !strings.Contains(err.Error(), "不明") && !strings.Contains(err.Error(), "未知") {
					t.Fatalf("Execute(%v) error = %q, want unknown-command style failure", args, err)
				}
			}
		})
	}
}

// TestRootCLI_HelpDoesNotListIntegration confirms the removed subtree
// is invisible from top-level help.
func TestRootCLI_HelpDoesNotListIntegration(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(--help) error = %v", err)
	}
	help := stdout.String()
	if strings.Contains(extractAvailableCommandsBlock(help), "integration") {
		t.Fatalf("traceary --help still lists integration:\n%s", help)
	}
}
