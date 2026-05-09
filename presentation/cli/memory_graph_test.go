package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

// TestRootCLI_MemoryGraphList_RejectsNegativeLimit guards the
// Codex-verifier MUST on #698 v2: only --limit=0 is allowed to
// disable the cap. A negative value must return an error rather
// than silently passing through to SQLite (where `LIMIT -1` reads
// as "no cap" and the CLI contract would quietly diverge).
func TestRootCLI_MemoryGraphList_RejectsNegativeLimit(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := newTestRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"memory", "admin", "graph", "list", "--limit", "-1"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute(memory graph list --limit -1) unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("expected error to mention --limit; got %v", err)
	}
}
