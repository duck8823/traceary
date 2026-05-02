package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_MemoryExport_DefaultIncludesGlobalAndReportsJSONCount(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{
		exportResult: apptypes.MemoryExportResult{
			Target:        apptypes.MemoryBridgeTargetCodex,
			Markdown:      "<!-- traceary-memories:begin:v1 -->\nmanaged\n<!-- traceary-memories:end -->\n",
			ExportedCount: 2,
		},
	}
	stdout := &bytes.Buffer{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "export",
		"--target", "codex",
		"--workspace", "github.com/duck8823/traceary",
		"--out", filepath.Join(t.TempDir(), "AGENTS.md"),
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	assertMemoryExportCall(t, memoryStub, true)
	if !strings.Contains(stdout.String(), `"exported_count": 2`) {
		t.Fatalf("stdout = %q; want exported_count 2", stdout.String())
	}
}

func TestRootCLI_MemoryExport_NoGlobalOptOut(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{
		exportResult: apptypes.MemoryExportResult{
			Target:        apptypes.MemoryBridgeTargetCodex,
			Markdown:      "<!-- traceary-memories:begin:v1 -->\nmanaged\n<!-- traceary-memories:end -->\n",
			ExportedCount: 1,
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "export",
		"--target", "codex",
		"--workspace", "github.com/duck8823/traceary",
		"--out", filepath.Join(t.TempDir(), "AGENTS.md"),
		"--no-global",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	assertMemoryExportCall(t, memoryStub, false)
}

func assertMemoryExportCall(t *testing.T, stub *memoryUsecaseStub, wantIncludeGlobal bool) {
	t.Helper()
	if len(stub.exportCalls) != 1 {
		t.Fatalf("export calls = %d, want 1", len(stub.exportCalls))
	}
	call := stub.exportCalls[0]
	if call.IncludeGlobal != wantIncludeGlobal {
		t.Fatalf("IncludeGlobal = %v, want %v", call.IncludeGlobal, wantIncludeGlobal)
	}
	if len(call.Scopes) != 1 {
		t.Fatalf("Scopes len = %d, want 1", len(call.Scopes))
	}
	if call.Scopes[0].Kind() != types.MemoryScopeKindWorkspace || call.Scopes[0].Key() != "github.com/duck8823/traceary" {
		t.Fatalf("Scopes[0] = %s:%s, want workspace:github.com/duck8823/traceary", call.Scopes[0].Kind(), call.Scopes[0].Key())
	}
}
