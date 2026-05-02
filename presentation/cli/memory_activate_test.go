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

func TestRootCLI_MemoryActivateDryRunPrintsPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationPlan: apptypes.MemoryActivationPlan{
			Target:     apptypes.MemoryBridgeTargetCodex,
			TargetPath: targetPath,
			Markdown:   "<!-- traceary-memories:begin:v1 -->\nplanned\n<!-- traceary-memories:end -->\n",
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
		"memory", "activate",
		"--target", "codex",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--dry-run",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "target: "+targetPath) || !strings.Contains(stdout.String(), "planned") {
		t.Fatalf("stdout = %q; want target path and planned content", stdout.String())
	}
	assertMemoryActivateCall(t, memoryStub, true)
}

func TestRootCLI_MemoryActivateRejectsNonDryRun(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "activate", "--target", "codex"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error without --dry-run")
	}
	if len(memoryStub.activationPlanCalls) != 0 {
		t.Fatalf("ActivatePlan should not be called without --dry-run")
	}
}

func assertMemoryActivateCall(t *testing.T, stub *memoryUsecaseStub, wantIncludeGlobal bool) {
	t.Helper()
	if len(stub.activationPlanCalls) != 1 {
		t.Fatalf("activation plan calls = %d, want 1", len(stub.activationPlanCalls))
	}
	call := stub.activationPlanCalls[0]
	if call.Target != apptypes.MemoryBridgeTargetCodex {
		t.Fatalf("Target = %q, want codex", call.Target)
	}
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
