package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func runStoreDedupe(t *testing.T, stub *storeManagementUsecaseStub, args ...string) (string, error) {
	t.Helper()
	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.WithStoreManagement(stub)).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"store", "dedupe", "content-events", "--db-path", "/tmp/traceary.db"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), err
}

func TestRootCLI_StoreDedupeContentEvents_DryRunText(t *testing.T) {
	stub := &storeManagementUsecaseStub{
		dedupeResult: apptypes.ContentEventDedupeResult{
			ScannedCount: 5,
			Groups: []apptypes.ContentEventDedupeGroup{
				{KeptEventID: "evt-a1", DuplicateEventIDs: []string{"evt-a2", "evt-a3"}, Kind: "prompt", Agent: "codex", SourceHook: "user_prompt_submit", GroupKey: "k"},
			},
			Skipped: []apptypes.ContentEventDedupeSkip{
				{GroupKey: "k2", EventIDs: []string{"evt-e1", "evt-e2"}, Reason: "skipped: malformed or unparseable created_at"},
			},
		},
	}
	out, err := runStoreDedupe(t, stub)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(stub.dedupeParams) != 1 {
		t.Fatalf("dedupeParams len = %d, want 1", len(stub.dedupeParams))
	}
	if stub.dedupeParams[0].Apply {
		t.Fatalf("default run set Apply=true, want dry-run")
	}
	if stub.dedupeParams[0].Agent != "codex" {
		t.Fatalf("default Agent = %q, want codex", stub.dedupeParams[0].Agent)
	}
	if !strings.Contains(out, "kept=evt-a1") || !strings.Contains(out, "duplicates=evt-a2,evt-a3") {
		t.Fatalf("missing group line in output:\n%s", out)
	}
	if !strings.Contains(out, "--apply") {
		t.Fatalf("dry-run output should mention --apply:\n%s", out)
	}
}

func TestRootCLI_StoreDedupeContentEvents_ClientAll(t *testing.T) {
	stub := &storeManagementUsecaseStub{}
	if _, err := runStoreDedupe(t, stub, "--client", "all"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.dedupeParams[0].Agent != "" {
		t.Fatalf("--client all Agent = %q, want empty", stub.dedupeParams[0].Agent)
	}
}

func TestRootCLI_StoreDedupeContentEvents_RejectsUnknownClient(t *testing.T) {
	stub := &storeManagementUsecaseStub{}
	if _, err := runStoreDedupe(t, stub, "--client", "bogus"); err == nil {
		t.Fatalf("expected error for unknown --client")
	}
}

func TestRootCLI_StoreDedupeContentEvents_ApplyJSON(t *testing.T) {
	stub := &storeManagementUsecaseStub{
		dedupeResult: apptypes.ContentEventDedupeResult{
			RunID:        "dedupe-abc",
			Applied:      true,
			ScannedCount: 4,
			Groups: []apptypes.ContentEventDedupeGroup{
				{KeptEventID: "evt-a1", DuplicateEventIDs: []string{"evt-a2"}, Kind: "prompt", Agent: "codex", GroupKey: "k"},
			},
		},
	}
	out, err := runStoreDedupe(t, stub, "--apply", "--json")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !stub.dedupeParams[0].Apply {
		t.Fatalf("--apply did not set Apply=true")
	}
	var payload struct {
		RunID      string `json:"run_id"`
		Applied    bool   `json:"applied"`
		MovedCount int    `json:"moved_count"`
		GroupCount int    `json:"group_count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, out)
	}
	if payload.RunID != "dedupe-abc" || !payload.Applied || payload.MovedCount != 1 || payload.GroupCount != 1 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestRootCLI_StoreDedupeContentEvents_Restore(t *testing.T) {
	stub := &storeManagementUsecaseStub{
		restoreResult: apptypes.ContentEventDedupeRestoreResult{RunID: "dedupe-abc", RestoredCount: 2},
	}
	out, err := runStoreDedupe(t, stub, "--restore", "dedupe-abc")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(stub.restoreRunIDs) != 1 || stub.restoreRunIDs[0] != "dedupe-abc" {
		t.Fatalf("restoreRunIDs = %v, want [dedupe-abc]", stub.restoreRunIDs)
	}
	if len(stub.dedupeParams) != 0 {
		t.Fatalf("restore must not call DedupeContentEvents")
	}
	if !strings.Contains(out, "dedupe-abc") || !strings.Contains(out, "2") {
		t.Fatalf("restore output missing run id/count:\n%s", out)
	}
}

func TestRootCLI_StoreDedupeContentEvents_ApplyAndRestoreConflict(t *testing.T) {
	stub := &storeManagementUsecaseStub{}
	if _, err := runStoreDedupe(t, stub, "--apply", "--restore", "dedupe-abc"); err == nil {
		t.Fatalf("expected error combining --apply and --restore")
	}
}
