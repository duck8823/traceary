package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	for _, must := range []string{"requires an interactive terminal", "traceary sessions --snapshot", "traceary memory inbox list", "traceary tui"} {
		if !strings.Contains(err.Error(), must) {
			t.Fatalf("error guidance missing %q:\n%s", must, err.Error())
		}
		if !strings.Contains(stdout.String(), must) {
			t.Fatalf("stdout guidance missing %q:\n%s", must, stdout.String())
		}
	}
}

func TestCockpitCommand_ResetStateDoesNotRunForNonTTY(t *testing.T) {
	t.Parallel()

	state := &cockpitStateSpy{}
	rootCmd := cli.NewRootCLI(cli.WithCockpitStateReader(state)).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"tui", "--reset-state"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute(tui --reset-state) error = nil, want non-TTY refusal")
	}
	if state.resetCalls != 0 {
		t.Fatalf("ResetCockpitState() calls = %d, want 0 for non-TTY execution", state.resetCalls)
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
	for _, must := range []string{"operator cockpit", "dashboard", "--db-path", "--reset-state", "top", "tail", "doctor", "memory review"} {
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

type cockpitStateSpy struct {
	resetCalls int
}

func (s *cockpitStateSpy) MemoryLastSeenAt(context.Context) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *cockpitStateSpy) EventLastSeenAt(context.Context) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *cockpitStateSpy) EventLastSeenIDs(context.Context) ([]string, bool, error) {
	return nil, false, nil
}

func (s *cockpitStateSpy) MarkMemoryLastSeenAt(context.Context, time.Time) error {
	return nil
}

func (s *cockpitStateSpy) MarkEventLastSeenAt(context.Context, time.Time, []string) error {
	return nil
}

func (s *cockpitStateSpy) ResetCockpitState(context.Context) error {
	s.resetCalls++
	return nil
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
