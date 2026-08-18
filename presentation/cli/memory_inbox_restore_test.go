package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

type memoryInboxRestoreStub struct {
	memoryUsecaseStub
	restored []types.MemoryID
}

func (s *memoryInboxRestoreStub) Restore(_ context.Context, memoryID types.MemoryID) (apptypes.MemoryDetails, error) {
	s.restored = append(s.restored, memoryID)
	return apptypes.MemoryDetails{}, nil
}

func TestRootCLI_MemoryInboxRestore_RestoresByID(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	stub := &memoryInboxRestoreStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "inbox", "restore",
		"--db-path", filepath.Join(t.TempDir(), "traceary.db"),
		"--ids", "mem-restore-1",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "restored 1 memory candidate(s)") {
		t.Fatalf("stdout = %q, want restored count", got)
	}
	if len(stub.restored) != 1 || stub.restored[0].String() != "mem-restore-1" {
		t.Fatalf("Restore() ids = %v, want [mem-restore-1]", stub.restored)
	}
}
