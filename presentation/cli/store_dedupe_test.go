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

func TestRootCLI_StoreDedupeContentEvents_ClientKimi(t *testing.T) {
	stub := &storeManagementUsecaseStub{}
	if _, err := runStoreDedupe(t, stub, "--client", "kimi"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.dedupeParams[0].Agent != "kimi" {
		t.Fatalf("--client kimi Agent = %q, want kimi", stub.dedupeParams[0].Agent)
	}
}

func TestRootCLI_StoreDedupeContentEvents_BatchSize(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "defaults to the batch size the store expects", args: nil, want: apptypes.DefaultContentEventDedupeBatchSize},
		{name: "explicit batch size is passed through", args: []string{"--batch-size", "25"}, want: 25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &storeManagementUsecaseStub{}
			if _, err := runStoreDedupe(t, stub, append([]string{"--apply"}, test.args...)...); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := stub.dedupeParams[0].BatchSize; got != test.want {
				t.Errorf("BatchSize = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRootCLI_StoreDedupeContentEvents_Purge(t *testing.T) {
	stub := &storeManagementUsecaseStub{
		purgeResult: apptypes.ContentEventDedupePurgeResult{RunID: "dedupe-abc", PurgedCount: 3, ReleasedBody: 4096},
	}
	out, err := runStoreDedupe(t, stub, "--purge", "dedupe-abc")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(stub.purgeRunIDs) != 1 || stub.purgeRunIDs[0] != "dedupe-abc" {
		t.Fatalf("purgeRunIDs = %v, want [dedupe-abc]", stub.purgeRunIDs)
	}
	if len(stub.dedupeParams) != 0 {
		t.Fatal("purge must not call DedupeContentEvents")
	}
	// Purge only returns pages to SQLite's free list, so the operator has to be
	// told that reclaiming disk needs VACUUM.
	for _, want := range []string{"dedupe-abc", "3", "4096", "VACUUM"} {
		if !strings.Contains(out, want) {
			t.Errorf("purge output missing %q:\n%s", want, out)
		}
	}
}

func TestRootCLI_StoreDedupeContentEvents_PurgeJSON(t *testing.T) {
	stub := &storeManagementUsecaseStub{
		purgeResult: apptypes.ContentEventDedupePurgeResult{RunID: "dedupe-abc", PurgedCount: 3, ReleasedBody: 4096},
	}
	out, err := runStoreDedupe(t, stub, "--purge", "dedupe-abc", "--json")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var payload struct {
		RunID             string `json:"run_id"`
		PurgedCount       int    `json:"purged_count"`
		ReleasedBodyBytes int64  `json:"released_body_bytes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, out)
	}
	if payload.RunID != "dedupe-abc" || payload.PurgedCount != 3 || payload.ReleasedBodyBytes != 4096 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

// Quarantine, restore, and purge are three different intents; asking for more
// than one at a time is an operator mistake, not something to resolve silently.
func TestRootCLI_StoreDedupeContentEvents_RejectsConflictingModes(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "apply with restore", args: []string{"--apply", "--restore", "dedupe-abc"}},
		{name: "apply with purge", args: []string{"--apply", "--purge", "dedupe-abc"}},
		{name: "restore with purge", args: []string{"--restore", "dedupe-abc", "--purge", "dedupe-abc"}},
		{name: "negative batch size", args: []string{"--apply", "--batch-size", "-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &storeManagementUsecaseStub{}
			if _, err := runStoreDedupe(t, stub, test.args...); err == nil {
				t.Errorf("runStoreDedupe(%v) = nil error, want failure", test.args)
			}
		})
	}
}
