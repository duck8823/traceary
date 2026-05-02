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

func TestRootCLI_MemoryActivateClaudeDryRunRendersComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationPlan: apptypes.MemoryActivationPlan{
			Target:     apptypes.MemoryBridgeTargetClaude,
			TargetPath: hostPath,
			HostContext: &apptypes.MemoryActivationComponent{
				Path:     hostPath,
				Existing: false,
				Markdown: "<!-- traceary-memory-import:begin:v1 -->\n@./.traceary/memories/claude.md\n<!-- traceary-memory-import:end -->\n",
				Action:   apptypes.MemoryActivationApplyCreated,
				State:    apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:     externalPath,
				Existing: false,
				Markdown: "<!-- traceary-memories:begin:v1 -->\nplanned\n<!-- traceary-memories:end -->\n",
				Action:   apptypes.MemoryActivationApplyCreated,
				State:    apptypes.MemoryActivationStatusMissing,
			},
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
		"--target", "claude",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--dry-run",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"target: claude",
		"external_memory: " + externalPath,
		"host_context: " + hostPath,
		"# external memory plan",
		"# host context plan",
		"@./.traceary/memories/claude.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
	if len(memoryStub.activationPlanCalls) != 1 {
		t.Fatalf("activation plan calls = %d, want 1", len(memoryStub.activationPlanCalls))
	}
	if memoryStub.activationPlanCalls[0].Target != apptypes.MemoryBridgeTargetClaude {
		t.Fatalf("Target = %q, want claude", memoryStub.activationPlanCalls[0].Target)
	}
}

func TestRootCLI_MemoryActivateClaudeDryRunDiffOrdersExternalFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationPlan: apptypes.MemoryActivationPlan{
			Target:     apptypes.MemoryBridgeTargetClaude,
			TargetPath: hostPath,
			Diff:       "--- " + externalPath + "\n+++ " + externalPath + " (planned)\n--- " + hostPath + "\n+++ " + hostPath + " (planned)\n",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:     hostPath,
				Existing: true,
				Diff:     "--- " + hostPath + "\n+++ " + hostPath + " (planned)\n",
				State:    apptypes.MemoryActivationStatusStale,
				Action:   apptypes.MemoryActivationApplyUpdated,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:     externalPath,
				Existing: true,
				Diff:     "--- " + externalPath + "\n+++ " + externalPath + " (planned)\n",
				State:    apptypes.MemoryActivationStatusStale,
				Action:   apptypes.MemoryActivationApplyUpdated,
			},
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
		"--target", "claude",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--dry-run",
		"--diff",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	externalIdx := strings.Index(out, "# external memory diff")
	hostIdx := strings.Index(out, "# host context diff")
	if externalIdx < 0 || hostIdx < 0 || externalIdx >= hostIdx {
		t.Fatalf("diff output must order external before host, externalIdx=%d hostIdx=%d out=%q", externalIdx, hostIdx, out)
	}
	if len(memoryStub.activationPlanCalls) != 1 {
		t.Fatalf("activation plan calls = %d, want 1", len(memoryStub.activationPlanCalls))
	}
	if !memoryStub.activationPlanCalls[0].Diff {
		t.Fatalf("plan call did not propagate Diff=true")
	}
}

func TestRootCLI_MemoryActivateClaudeStatusJSONIncludesComponentsAndCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetClaude,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 1,
			Message:        "host context import stub and external memory file are missing",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
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
		"--target", "claude",
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
		ActivatedCount int    `json:"activated_count"`
		DryRunCommand  string `json:"dry_run_command"`
		ApplyCommand   string `json:"apply_command"`
		HostContext    *struct {
			Path  string `json:"path"`
			State string `json:"state"`
		} `json:"host_context"`
		ExternalMemory *struct {
			Path  string `json:"path"`
			State string `json:"state"`
		} `json:"external_memory"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.Target != "claude" || payload.TargetPath != hostPath || payload.State != "missing" {
		t.Fatalf("payload = %+v, want claude/missing", payload)
	}
	if payload.HostContext == nil || payload.HostContext.Path != hostPath || payload.HostContext.State != "missing" {
		t.Fatalf("host_context = %+v, want missing component", payload.HostContext)
	}
	if payload.ExternalMemory == nil || payload.ExternalMemory.Path != externalPath || payload.ExternalMemory.State != "missing" {
		t.Fatalf("external_memory = %+v, want missing component", payload.ExternalMemory)
	}
	if !strings.Contains(payload.DryRunCommand, "--target claude") || !strings.Contains(payload.DryRunCommand, "--dry-run --diff") {
		t.Fatalf("dry_run_command = %q, want claude dry-run command", payload.DryRunCommand)
	}
	if !strings.Contains(payload.ApplyCommand, "--target claude") || !strings.Contains(payload.ApplyCommand, "--apply") {
		t.Fatalf("apply_command = %q, want claude apply command", payload.ApplyCommand)
	}
}

func TestRootCLI_MemoryActivateClaudeStatusTextSurfacesApplyRemediation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetClaude,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 1,
			Message:        "host context import stub and external memory file are missing",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
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
		"--target", "claude",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--status",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "next_dry_run:") {
		t.Fatalf("stdout missing next_dry_run remediation: %q", out)
	}
	if !strings.Contains(out, "next_apply:") {
		t.Fatalf("stdout missing next_apply remediation for claude: %q", out)
	}
	if !strings.Contains(out, "--target claude") || !strings.Contains(out, "--apply") {
		t.Fatalf("next_apply must reference claude --apply: %q", out)
	}
}

func TestRootCLI_MemoryActivateClaudeApplyPrintsPairResult(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationResult: apptypes.MemoryActivationApplyResult{
			Target:         apptypes.MemoryBridgeTargetClaude,
			TargetPath:     hostPath,
			Action:         apptypes.MemoryActivationApplyCreated,
			ActivatedCount: 2,
			HostContext: &apptypes.MemoryActivationComponent{
				Path:   hostPath,
				Action: apptypes.MemoryActivationApplyCreated,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:   externalPath,
				Action: apptypes.MemoryActivationApplyCreated,
			},
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
		"--target", "claude",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--apply",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"target: claude",
		"action: created",
		"external_memory: " + externalPath + " (action: created)",
		"host_context: " + hostPath + " (action: created)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
	if len(memoryStub.activationCalls) != 1 {
		t.Fatalf("activation calls = %d, want 1", len(memoryStub.activationCalls))
	}
	if memoryStub.activationCalls[0].Target != apptypes.MemoryBridgeTargetClaude {
		t.Fatalf("Target = %q, want claude", memoryStub.activationCalls[0].Target)
	}
}

func TestRootCLI_MemoryActivateClaudeApplyJSONIncludesComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "CLAUDE.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "claude.md")
	memoryStub := &memoryUsecaseStub{
		activationResult: apptypes.MemoryActivationApplyResult{
			Target:         apptypes.MemoryBridgeTargetClaude,
			TargetPath:     hostPath,
			Action:         apptypes.MemoryActivationApplyUpdated,
			ActivatedCount: 3,
			HostContext: &apptypes.MemoryActivationComponent{
				Path:   hostPath,
				Action: apptypes.MemoryActivationApplyNoop,
				State:  apptypes.MemoryActivationStatusInSync,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:   externalPath,
				Action: apptypes.MemoryActivationApplyUpdated,
				State:  apptypes.MemoryActivationStatusInSync,
			},
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
		"--target", "claude",
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
		ActivatedCount int    `json:"activated_count"`
		HostContext    *struct {
			Path   string `json:"path"`
			Action string `json:"action"`
			State  string `json:"state"`
		} `json:"host_context"`
		ExternalMemory *struct {
			Path   string `json:"path"`
			Action string `json:"action"`
			State  string `json:"state"`
		} `json:"external_memory"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.Target != "claude" || payload.Action != "updated" || payload.ActivatedCount != 3 {
		t.Fatalf("payload = %+v, want claude/updated/3", payload)
	}
	if payload.HostContext == nil || payload.HostContext.Action != "noop" || payload.HostContext.Path != hostPath {
		t.Fatalf("host_context = %+v, want noop at %s", payload.HostContext, hostPath)
	}
	if payload.ExternalMemory == nil || payload.ExternalMemory.Action != "updated" || payload.ExternalMemory.Path != externalPath {
		t.Fatalf("external_memory = %+v, want updated at %s", payload.ExternalMemory, externalPath)
	}
	if payload.HostContext.State != "in_sync" || payload.ExternalMemory.State != "in_sync" {
		t.Fatalf("component states = host=%q external=%q, want in_sync after apply", payload.HostContext.State, payload.ExternalMemory.State)
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

func TestRootCLI_MemoryActivateGeminiDryRunRendersComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "GEMINI.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationPlan: apptypes.MemoryActivationPlan{
			Target:     apptypes.MemoryBridgeTargetGemini,
			TargetPath: hostPath,
			HostContext: &apptypes.MemoryActivationComponent{
				Path:     hostPath,
				Existing: false,
				Markdown: "<!-- traceary-memory-import:begin:v1 -->\n@./.traceary/memories/gemini.md\n<!-- traceary-memory-import:end -->\n",
				Action:   apptypes.MemoryActivationApplyCreated,
				State:    apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:     externalPath,
				Existing: false,
				Markdown: "<!-- traceary-memories:begin:v1 -->\nplanned\n<!-- traceary-memories:end -->\n",
				Action:   apptypes.MemoryActivationApplyCreated,
				State:    apptypes.MemoryActivationStatusMissing,
			},
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
		"--target", "gemini",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--dry-run",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"target: gemini",
		"external_memory: " + externalPath,
		"host_context: " + hostPath,
		"# external memory plan",
		"# host context plan",
		"@./.traceary/memories/gemini.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
	if len(memoryStub.activationPlanCalls) != 1 {
		t.Fatalf("activation plan calls = %d, want 1", len(memoryStub.activationPlanCalls))
	}
	if memoryStub.activationPlanCalls[0].Target != apptypes.MemoryBridgeTargetGemini {
		t.Fatalf("Target = %q, want gemini", memoryStub.activationPlanCalls[0].Target)
	}
}

func TestRootCLI_MemoryActivateGeminiDryRunDiffOrdersExternalFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "GEMINI.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationPlan: apptypes.MemoryActivationPlan{
			Target:     apptypes.MemoryBridgeTargetGemini,
			TargetPath: hostPath,
			Diff:       "--- " + externalPath + "\n+++ " + externalPath + " (planned)\n--- " + hostPath + "\n+++ " + hostPath + " (planned)\n",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:     hostPath,
				Existing: true,
				Diff:     "--- " + hostPath + "\n+++ " + hostPath + " (planned)\n",
				State:    apptypes.MemoryActivationStatusStale,
				Action:   apptypes.MemoryActivationApplyUpdated,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:     externalPath,
				Existing: true,
				Diff:     "--- " + externalPath + "\n+++ " + externalPath + " (planned)\n",
				State:    apptypes.MemoryActivationStatusStale,
				Action:   apptypes.MemoryActivationApplyUpdated,
			},
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
		"--target", "gemini",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--dry-run",
		"--diff",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	externalIdx := strings.Index(out, "# external memory diff")
	hostIdx := strings.Index(out, "# host context diff")
	if externalIdx < 0 || hostIdx < 0 || externalIdx >= hostIdx {
		t.Fatalf("diff output must order external before host, externalIdx=%d hostIdx=%d out=%q", externalIdx, hostIdx, out)
	}
	if !memoryStub.activationPlanCalls[0].Diff {
		t.Fatalf("plan call did not propagate Diff=true")
	}
}

func TestRootCLI_MemoryActivateGeminiStatusJSONIncludesApplyCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "GEMINI.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 1,
			Message:        "host context import stub and external memory file are missing",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
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
		"--target", "gemini",
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
		ActivatedCount int    `json:"activated_count"`
		DryRunCommand  string `json:"dry_run_command"`
		ApplyCommand   string `json:"apply_command"`
		HostContext    *struct {
			Path  string `json:"path"`
			State string `json:"state"`
		} `json:"host_context"`
		ExternalMemory *struct {
			Path  string `json:"path"`
			State string `json:"state"`
		} `json:"external_memory"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.Target != "gemini" || payload.State != "missing" {
		t.Fatalf("payload = %+v, want gemini/missing", payload)
	}
	if !strings.Contains(payload.DryRunCommand, "--target gemini") || !strings.Contains(payload.DryRunCommand, "--dry-run --diff") {
		t.Fatalf("dry_run_command = %q, want gemini dry-run command", payload.DryRunCommand)
	}
	if !strings.Contains(payload.ApplyCommand, "--target gemini") || !strings.Contains(payload.ApplyCommand, "--apply") {
		t.Fatalf("apply_command = %q, want gemini apply remediation", payload.ApplyCommand)
	}
	if payload.HostContext == nil || payload.HostContext.Path != hostPath {
		t.Fatalf("host_context = %+v, want gemini host", payload.HostContext)
	}
	if payload.ExternalMemory == nil || payload.ExternalMemory.Path != externalPath {
		t.Fatalf("external_memory = %+v, want gemini external", payload.ExternalMemory)
	}
}

func TestRootCLI_MemoryActivateGeminiStatusTextIncludesApplyRemediation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "GEMINI.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationStatus: apptypes.MemoryActivationStatusResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			State:          apptypes.MemoryActivationStatusMissing,
			Existing:       false,
			ActivatedCount: 1,
			Message:        "host context import stub and external memory file are missing",
			HostContext: &apptypes.MemoryActivationComponent{
				Path:  hostPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:  externalPath,
				State: apptypes.MemoryActivationStatusMissing,
			},
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
		"--target", "gemini",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--status",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "next_dry_run:") {
		t.Fatalf("stdout missing next_dry_run remediation: %q", out)
	}
	if !strings.Contains(out, "next_apply:") {
		t.Fatalf("stdout missing next_apply remediation: %q", out)
	}
	if !strings.Contains(out, "--target gemini") {
		t.Fatalf("remediation must reference --target gemini: %q", out)
	}
}

func TestRootCLI_MemoryActivateGeminiApplyPrintsPairResult(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "GEMINI.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationResult: apptypes.MemoryActivationApplyResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			Action:         apptypes.MemoryActivationApplyCreated,
			ActivatedCount: 2,
			HostContext: &apptypes.MemoryActivationComponent{
				Path:   hostPath,
				Action: apptypes.MemoryActivationApplyCreated,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:   externalPath,
				Action: apptypes.MemoryActivationApplyCreated,
			},
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
		"--target", "gemini",
		"--root", root,
		"--workspace", "github.com/duck8823/traceary",
		"--apply",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"target: gemini",
		"action: created",
		"external_memory: " + externalPath + " (action: created)",
		"host_context: " + hostPath + " (action: created)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
	if len(memoryStub.activationCalls) != 1 {
		t.Fatalf("activation calls = %d, want 1", len(memoryStub.activationCalls))
	}
	if memoryStub.activationCalls[0].Target != apptypes.MemoryBridgeTargetGemini {
		t.Fatalf("Target = %q, want gemini", memoryStub.activationCalls[0].Target)
	}
}

func TestRootCLI_MemoryActivateGeminiApplyJSONIncludesComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hostPath := filepath.Join(root, "GEMINI.md")
	externalPath := filepath.Join(root, ".traceary", "memories", "gemini.md")
	memoryStub := &memoryUsecaseStub{
		activationResult: apptypes.MemoryActivationApplyResult{
			Target:         apptypes.MemoryBridgeTargetGemini,
			TargetPath:     hostPath,
			Action:         apptypes.MemoryActivationApplyUpdated,
			ActivatedCount: 3,
			HostContext: &apptypes.MemoryActivationComponent{
				Path:   hostPath,
				Action: apptypes.MemoryActivationApplyNoop,
				State:  apptypes.MemoryActivationStatusInSync,
			},
			ExternalMemory: &apptypes.MemoryActivationComponent{
				Path:   externalPath,
				Action: apptypes.MemoryActivationApplyUpdated,
				State:  apptypes.MemoryActivationStatusInSync,
			},
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
		"--target", "gemini",
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
		ActivatedCount int    `json:"activated_count"`
		HostContext    *struct {
			Path   string `json:"path"`
			Action string `json:"action"`
			State  string `json:"state"`
		} `json:"host_context"`
		ExternalMemory *struct {
			Path   string `json:"path"`
			Action string `json:"action"`
			State  string `json:"state"`
		} `json:"external_memory"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%s", err, stdout.String())
	}
	if payload.Target != "gemini" || payload.Action != "updated" || payload.ActivatedCount != 3 {
		t.Fatalf("payload = %+v, want gemini/updated/3", payload)
	}
	if payload.HostContext == nil || payload.HostContext.Action != "noop" || payload.HostContext.Path != hostPath {
		t.Fatalf("host_context = %+v, want noop at %s", payload.HostContext, hostPath)
	}
	if payload.ExternalMemory == nil || payload.ExternalMemory.Action != "updated" || payload.ExternalMemory.Path != externalPath {
		t.Fatalf("external_memory = %+v, want updated at %s", payload.ExternalMemory, externalPath)
	}
	if payload.HostContext.State != "in_sync" || payload.ExternalMemory.State != "in_sync" {
		t.Fatalf("component states = host=%q external=%q, want in_sync after apply", payload.HostContext.State, payload.ExternalMemory.State)
	}
}
