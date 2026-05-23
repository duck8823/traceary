package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestCockpitCommand_RefusesNonTTYThroughCobra(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"tui"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute(tui) error = nil, want non-TTY refusal")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("expected error implementing ExitCode(); got %T (%v)", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Fatalf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	for _, must := range []string{"requires an interactive terminal", "traceary top --snapshot", "traceary memory inbox list", "traceary tui"} {
		if !strings.Contains(err.Error(), must) {
			t.Fatalf("error guidance missing %q:\n%s", must, err.Error())
		}
		if !strings.Contains(stdout.String(), must) {
			t.Fatalf("stdout guidance missing %q:\n%s", must, stdout.String())
		}
	}
}

func TestCockpitCommand_HelpAndAliasAreDiscoverable(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"tui", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(tui --help) error = %v", err)
	}
	help := stdout.String()
	for _, must := range []string{"operator cockpit", "dashboard", "--db-path", "top", "tail", "doctor", "memory review"} {
		if !strings.Contains(help, must) {
			t.Fatalf("tui --help missing %q:\n%s", must, help)
		}
	}

	rootHelp := &bytes.Buffer{}
	rootCmd = newTestRootCLI().Command()
	rootCmd.SetOut(rootHelp)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(--help) error = %v", err)
	}
	if !strings.Contains(rootHelp.String(), "tui") {
		t.Fatalf("root help does not list tui command:\n%s", rootHelp.String())
	}
}

func TestCockpitCommand_NoArgsCompatibility(t *testing.T) {
	t.Parallel()

	rootCmd := cli.NewRootCLI().Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(no args) error = %v", err)
	}
	if strings.Contains(stdout.String(), "Traceary cockpit requires") {
		t.Fatalf("bare traceary should not launch or refuse the cockpit; stdout:\n%s", stdout.String())
	}
}
