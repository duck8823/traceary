package cli_test

import (
	"bytes"
	"encoding/json"
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
		t.Fatalf("expected error without --dry-run or --apply")
	}
	if len(memoryStub.activationPlanCalls) != 0 {
		t.Fatalf("ActivatePlan should not be called without --dry-run")
	}
	if len(memoryStub.activationCalls) != 0 {
		t.Fatalf("Activate should not be called without --apply")
	}
}

func TestRootCLI_MemoryActivateApplyPrintsSummary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationResult: apptypes.MemoryActivationApplyResult{
			Target:         apptypes.MemoryBridgeTargetCodex,
			TargetPath:     targetPath,
			Action:         apptypes.MemoryActivationApplyCreated,
			ActivatedCount: 2,
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
		"--apply",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "target: "+targetPath) || !strings.Contains(out, "activated_count: 2") || !strings.Contains(out, "action: created") {
		t.Fatalf("stdout = %q; want activation summary", out)
	}
	assertMemoryApplyCall(t, memoryStub, true)
	if len(memoryStub.activationPlanCalls) != 0 {
		t.Fatalf("ActivatePlan should not be called in apply mode")
	}
}

func TestRootCLI_MemoryActivateApplyJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationResult: apptypes.MemoryActivationApplyResult{
			Target:         apptypes.MemoryBridgeTargetCodex,
			TargetPath:     targetPath,
			Action:         apptypes.MemoryActivationApplyNoop,
			Existing:       true,
			ActivatedCount: 3,
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
		"--apply",
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var payload struct {
		Target         string `json:"target"`
		TargetPath     string `json:"target_path"`
		Action         string `json:"action"`
		Existing       bool   `json:"existing"`
		ActivatedCount int    `json:"activated_count"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.Target != "codex" || payload.TargetPath != targetPath || payload.Action != "noop" || !payload.Existing || payload.ActivatedCount != 3 {
		t.Fatalf("payload = %+v, want codex/noop existing count", payload)
	}
}

func TestRootCLI_MemoryActivateRejectsDiffWithApply(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "activate", "--target", "codex", "--apply", "--diff"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for --diff with --apply")
	}
	if len(memoryStub.activationCalls) != 0 || len(memoryStub.activationPlanCalls) != 0 {
		t.Fatalf("memory usecase should not be called for invalid mode")
	}
}

func TestRootCLI_MemoryActivateStatusJSONIncludesCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetCodex,
			TargetPath:     targetPath,
			State:          apptypes.MemoryActivationStatusStale,
			Existing:       true,
			ActivatedCount: 4,
			Message:        "Traceary managed memory block differs from the current accepted memories",
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
		"--status",
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var payload struct {
		Target         string `json:"target"`
		TargetPath     string `json:"target_path"`
		State          string `json:"state"`
		Existing       bool   `json:"existing"`
		ActivatedCount int    `json:"activated_count"`
		DryRunCommand  string `json:"dry_run_command"`
		ApplyCommand   string `json:"apply_command"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.State != "stale" || payload.TargetPath != targetPath || !payload.Existing || payload.ActivatedCount != 4 {
		t.Fatalf("payload = %+v, want stale status", payload)
	}
	if !strings.Contains(payload.DryRunCommand, "--dry-run --diff") || !strings.Contains(payload.ApplyCommand, "--apply") {
		t.Fatalf("payload commands = dry-run %q apply %q", payload.DryRunCommand, payload.ApplyCommand)
	}
	assertMemoryStatusCall(t, memoryStub, true)
}

func TestRootCLI_MemoryActivateStatusTextOmitsCommandsWhenInSync(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetCodex,
			TargetPath:     targetPath,
			State:          apptypes.MemoryActivationStatusInSync,
			Existing:       true,
			ActivatedCount: 1,
			Message:        "activation target is in sync",
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
		"--status",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "state: in_sync") || strings.Contains(out, "next_apply:") {
		t.Fatalf("stdout = %q; want in_sync without remediation commands", out)
	}
	assertMemoryStatusCall(t, memoryStub, true)
}

func TestRootCLI_MemoryActivateStatusOmitsCommandsWhenInvalid(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetPath := filepath.Join(root, "traceary.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetCodex,
			TargetPath:     targetPath,
			State:          apptypes.MemoryActivationStatusInvalid,
			Existing:       true,
			ActivatedCount: 2,
			Message:        "newer managed block version",
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
		"--status",
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var payload struct {
		State         string `json:"state"`
		DryRunCommand string `json:"dry_run_command"`
		ApplyCommand  string `json:"apply_command"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.State != "invalid" || payload.DryRunCommand != "" || payload.ApplyCommand != "" {
		t.Fatalf("payload = %+v, want invalid status without remediation commands", payload)
	}
	assertMemoryStatusCall(t, memoryStub, true)
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

func assertMemoryStatusCall(t *testing.T, stub *memoryUsecaseStub, wantIncludeGlobal bool) {
	t.Helper()
	if len(stub.activationStatusCalls) != 1 {
		t.Fatalf("activation status calls = %d, want 1", len(stub.activationStatusCalls))
	}
	call := stub.activationStatusCalls[0]
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

func assertMemoryApplyCall(t *testing.T, stub *memoryUsecaseStub, wantIncludeGlobal bool) {
	t.Helper()
	if len(stub.activationCalls) != 1 {
		t.Fatalf("activation calls = %d, want 1", len(stub.activationCalls))
	}
	call := stub.activationCalls[0]
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
