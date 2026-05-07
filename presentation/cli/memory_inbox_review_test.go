package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestMemoryInboxReview_RefusesNonTTYThroughCobra runs the command end-to-end
// against a buffered stdout (which is not a TTY) and verifies the resulting
// error carries the expected exit code and guidance. Using cobra's writer
// path keeps this aligned with how `traceary memory inbox review` is invoked
// in production — the only thing the test bypasses is the actual pty.
func TestMemoryInboxReview_RefusesNonTTYThroughCobra(t *testing.T) {
	t.Parallel()
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "review", "--db-path", t.TempDir() + "/t.db"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected non-TTY refusal, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("expected error implementing ExitCode(); got %T (%v)", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Fatalf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	msg := err.Error()
	for _, must := range []string{"memory inbox list", "memory inbox accept", "memory inbox reject"} {
		if !strings.Contains(msg, must) {
			t.Fatalf("non-TTY guidance missing %q; got:\n%s", must, msg)
		}
	}
}

// TestMemoryInboxReview_HelpListsActions guards against accidental
// regressions of the inline help text — operators rely on `--help` to
// discover the action surface (accept / reject / skip / edit / view /
// help / quit) before launching the interactive walk-through.
func TestMemoryInboxReview_HelpListsActions(t *testing.T) {
	t.Parallel()
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "review", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help should succeed, got %v", err)
	}
	help := stdout.String()
	for _, must := range []string{"--workspace", "--agent", "--session-family", "--type", "--source", "--include-hidden", "--limit"} {
		if !strings.Contains(help, must) {
			t.Fatalf("review --help missing flag %q; got:\n%s", must, help)
		}
	}
}
