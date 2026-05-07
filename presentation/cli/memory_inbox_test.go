package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

var (
	errSyntheticAcceptFailure = errors.New("synthetic accept failure")
	errSyntheticRejectFailure = errors.New("synthetic reject failure")
)

func buildInboxCandidateDetails(t *testing.T, id string, fact string, source domtypes.MemorySource) apptypes.MemoryDetails {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		source,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L2")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{evidence}, nil)
}

func TestMemoryInboxList_TextOutput(t *testing.T) {
	t.Parallel()

	imported := buildInboxCandidateDetails(t, "memory-1", "prefer bulleted commits", domtypes.MemorySourceImported)
	manual := buildInboxCandidateDetails(t, "memory-2", "keep CI green", domtypes.MemorySourceManual)
	memoryStub := &memoryUsecaseStub{
		listResult:  []apptypes.MemorySummary{imported.Summary(), manual.Summary()},
		showDetails: imported,
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "list", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "MEMORY_ID\tTYPE\tSCOPE\tSOURCE\tEVIDENCE\tARTIFACT\tFACT") {
		t.Fatalf("expected inbox header, got %q", out)
	}
	// Status filter must be pinned to candidate on the list call.
	if got := memoryStub.listCriteria.Statuses(); len(got) != 1 || got[0] != domtypes.MemoryStatusCandidate {
		t.Fatalf("inbox list should filter to candidate status, got %v", got)
	}
}

func TestMemoryInboxList_SourceFilterPropagatesToCriteria(t *testing.T) {
	t.Parallel()

	imported := buildInboxCandidateDetails(t, "memory-i", "from codex", domtypes.MemorySourceImported)
	memoryStub := &memoryUsecaseStub{
		// The SQL datasource is responsible for honouring criteria.Sources();
		// the stub pre-filters so the test only verifies that the CLI hands
		// the right sources into the criteria.
		listResult:  []apptypes.MemorySummary{imported.Summary()},
		showDetails: imported,
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "list", "--db-path", t.TempDir() + "/t.db", "--source", "imported", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var items []struct {
		Summary struct {
			MemoryID string `json:"memory_id"`
			Source   string `json:"source"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v (body=%s)", err, stdout.String())
	}
	if len(items) != 1 || items[0].Summary.Source != "imported" {
		t.Fatalf("expected one imported memory, got %+v", items)
	}
	if got := memoryStub.listCriteria.Sources(); len(got) != 1 || got[0] != domtypes.MemorySourceImported {
		t.Fatalf("inbox list should pass --source into criteria, got %v", got)
	}
}

func TestMemoryInboxList_DefaultExcludesExtractedHidden(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "list", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := memoryStub.listCriteria.Sources()
	if len(got) == 0 {
		t.Fatalf("default inbox list must constrain Sources to exclude extracted-hidden, got empty filter")
	}
	for _, s := range got {
		if s == domtypes.MemorySourceExtractedHidden {
			t.Fatalf("default inbox list must not include extracted-hidden in Sources, got %v", got)
		}
	}
}

func TestMemoryInboxList_IncludeHiddenSkipsDefaultFilter(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "list", "--include-hidden", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := memoryStub.listCriteria.Sources(); len(got) != 0 {
		t.Fatalf("--include-hidden should not add a Sources filter, got %v", got)
	}
}

// TestMemoryInboxList_RememberIntentPriorityFlagSetOnCriteria pins that the
// inbox view enables the remember-intent priority flag at the query layer
// so pagination is consistent with the displayed priority order. A
// post-fetch in-memory sort would only re-order the current page and could
// hide remember-intent rows past page boundaries.
func TestMemoryInboxList_RememberIntentPriorityFlagSetOnCriteria(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "list", "--db-path", t.TempDir() + "/t.db", "--limit", "5", "--offset", "10"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !memoryStub.listCriteria.RememberIntentPriority() {
		t.Fatalf("inbox list must enable RememberIntentPriority on the criteria so ordering is applied before LIMIT/OFFSET")
	}
}

func TestMemoryInboxAccept_BatchIDs(t *testing.T) {
	t.Parallel()

	ok := buildInboxCandidateDetails(t, "ok-id", "fact", domtypes.MemorySourceManual)
	acceptedDetails := apptypes.MemoryDetailsOf(
		mustSummaryWithStatus(t, "ok-id", domtypes.MemoryStatusAccepted),
		nil, nil,
	)
	memoryStub := &memoryUsecaseStub{
		acceptDetails: acceptedDetails,
		showDetails:   ok,
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "accept", "--db-path", t.TempDir() + "/t.db", "--ids", "ok-id,ok-id,  ok-id"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Duplicate ids collapse to a single Accept call.
	if got := memoryStub.acceptCallCount; got != 1 {
		t.Fatalf("Accept should be called once after dedupe, got %d", got)
	}
	out := stdout.String()
	if !strings.Contains(out, "action=accept processed=1 failures=0") {
		t.Fatalf("unexpected summary in stdout: %q", out)
	}
}

// TestMemoryInboxAccept_PositionalID pins #923: a single positional id
// resolves through the same Accept path as `--ids` so operators can write
// the natural `traceary memory inbox accept <id>` form interactively.
func TestMemoryInboxAccept_PositionalID(t *testing.T) {
	t.Parallel()

	accepted := apptypes.MemoryDetailsOf(
		mustSummaryWithStatus(t, "memory-pos", domtypes.MemoryStatusAccepted),
		nil, nil,
	)
	memoryStub := &memoryUsecaseStub{
		acceptDetails: accepted,
		showDetails:   buildInboxCandidateDetails(t, "memory-pos", "fact", domtypes.MemorySourceManual),
	}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "accept", "memory-pos", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := memoryStub.acceptCallCount; got != 1 {
		t.Fatalf("Accept should be called once for positional id, got %d", got)
	}
	if got := memoryStub.acceptCall.memoryID.String(); got != "memory-pos" {
		t.Fatalf("Accept memoryID = %q, want memory-pos", got)
	}
	if !strings.Contains(stdout.String(), "action=accept processed=1 failures=0") {
		t.Fatalf("unexpected summary: %q", stdout.String())
	}
}

func TestMemoryInboxAccept_PositionalJSON(t *testing.T) {
	t.Parallel()

	accepted := apptypes.MemoryDetailsOf(
		mustSummaryWithStatus(t, "memory-json", domtypes.MemoryStatusAccepted),
		nil, nil,
	)
	memoryStub := &memoryUsecaseStub{acceptDetails: accepted}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "accept", "memory-json", "--json", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var payload struct {
		Action    string `json:"action"`
		Processed []struct {
			Summary struct {
				MemoryID string `json:"memory_id"`
				Status   string `json:"status"`
			} `json:"summary"`
		} `json:"processed"`
		Failures []any `json:"failures"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v (body=%s)", err, stdout.String())
	}
	if payload.Action != "accept" {
		t.Fatalf("payload.Action = %q, want accept", payload.Action)
	}
	if len(payload.Processed) != 1 || payload.Processed[0].Summary.MemoryID != "memory-json" {
		t.Fatalf("unexpected processed payload: %+v", payload.Processed)
	}
	if len(payload.Failures) != 0 {
		t.Fatalf("unexpected failures: %+v", payload.Failures)
	}
}

func TestMemoryInboxReject_PositionalAndIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "positional",
			args: []string{"memory", "inbox", "reject", "memory-x"},
		},
		{
			name: "ids flag",
			args: []string{"memory", "inbox", "reject", "--ids", "memory-x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rejected := apptypes.MemoryDetailsOf(
				mustSummaryWithStatus(t, "memory-x", domtypes.MemoryStatusRejected),
				nil, nil,
			)
			memoryStub := &memoryUsecaseStub{rejectDetails: rejected}
			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(memoryStub),
			)
			cmd := root.Command()
			stdout := &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(&bytes.Buffer{})
			args := append([]string{}, tc.args...)
			args = append(args, "--db-path", t.TempDir()+"/t.db")
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if memoryStub.rejectCallCount != 1 {
				t.Fatalf("Reject should be called once, got %d", memoryStub.rejectCallCount)
			}
			if got := memoryStub.rejectCall.memoryID.String(); got != "memory-x" {
				t.Fatalf("Reject memoryID = %q, want memory-x", got)
			}
			if !strings.Contains(stdout.String(), "action=reject processed=1 failures=0") {
				t.Fatalf("unexpected summary: %q", stdout.String())
			}
		})
	}
}

// TestMemoryInboxAccept_IDOnlyPositionalSingle pins the v0.14 contract that
// the canonical `memory inbox accept` is a strict superset of the old
// flat `memory accept <memory-id> --id-only`: a single positional id with
// --id-only prints exactly that id on stdout and nothing else.
func TestMemoryInboxAccept_IDOnlyPositionalSingle(t *testing.T) {
	t.Parallel()

	accepted := apptypes.MemoryDetailsOf(
		mustSummaryWithStatus(t, "memory-only", domtypes.MemoryStatusAccepted),
		nil, nil,
	)
	memoryStub := &memoryUsecaseStub{acceptDetails: accepted}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"memory", "inbox", "accept", "memory-only", "--id-only", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := strings.TrimRight(stdout.String(), "\n"); got != "memory-only" {
		t.Fatalf("stdout = %q, want only the memory id", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr on success, got %q", stderr.String())
	}
}

// TestMemoryInboxReject_IDOnlyPositionalSingle is the matching contract
// pin for `memory inbox reject <id> --id-only`.
func TestMemoryInboxReject_IDOnlyPositionalSingle(t *testing.T) {
	t.Parallel()

	rejected := apptypes.MemoryDetailsOf(
		mustSummaryWithStatus(t, "memory-only", domtypes.MemoryStatusRejected),
		nil, nil,
	)
	memoryStub := &memoryUsecaseStub{rejectDetails: rejected}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"memory", "inbox", "reject", "memory-only", "--id-only", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := strings.TrimRight(stdout.String(), "\n"); got != "memory-only" {
		t.Fatalf("stdout = %q, want only the memory id", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr on success, got %q", stderr.String())
	}
}

// TestMemoryInboxAccept_IDOnlyBatchPrintsOnePerRow pins the deterministic
// batch behavior for --id-only: when --ids carries multiple entries that
// all succeed, stdout has one id per processed row in input order.
func TestMemoryInboxAccept_IDOnlyBatchPrintsOnePerRow(t *testing.T) {
	t.Parallel()

	// Stub returns the same details for every Accept call so the test
	// pins the row-count behavior, not the per-row id mapping (which the
	// usecase wires; the CLI just walks the result list).
	accepted := apptypes.MemoryDetailsOf(
		mustSummaryWithStatus(t, "memory-batch", domtypes.MemoryStatusAccepted),
		nil, nil,
	)
	memoryStub := &memoryUsecaseStub{acceptDetails: accepted}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"memory", "inbox", "accept", "--ids", "id-1,id-2", "--id-only", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 stdout lines for 2 accepted ids, got %d (%q)", len(lines), stdout.String())
	}
	for _, line := range lines {
		if line != "memory-batch" {
			t.Fatalf("each line should print the resulting memory id, got %q", line)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr on success, got %q", stderr.String())
	}
}

// TestMemoryInboxAccept_IDOnlyJSONMutuallyExclusive pins that --id-only
// and --json reject combined use, matching the existing memory write
// commands so scripted callers do not get conflicting output shapes.
func TestMemoryInboxAccept_IDOnlyJSONMutuallyExclusive(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "accept", "memory-x", "--id-only", "--json", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when --id-only and --json are combined")
	}
}

// TestMemoryInboxAccept_IDOnlyFailureReturnsError pins that --id-only
// surfaces failures: the failing id appears on stderr and Execute
// returns a non-nil error so scripts checking exit code do not silently
// swallow per-id failures (matching the old `memory accept <id>
// --id-only` contract).
func TestMemoryInboxAccept_IDOnlyFailureReturnsError(t *testing.T) {
	t.Parallel()

	memoryStub := &memoryUsecaseStub{acceptErr: errSyntheticAcceptFailure}
	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(memoryStub),
	)
	cmd := root.Command()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"memory", "inbox", "accept", "memory-fail", "--id-only", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when an Accept call fails under --id-only")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout when no row succeeds, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "FAILED\tmemory-fail\t") {
		t.Fatalf("expected FAILED stderr line for the failing id, got %q", stderr.String())
	}
}

func TestMemoryInboxBatch_TextFailureReturnsError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		args        []string
		memoryStub  *memoryUsecaseStub
		wantAction  string
		wantFailure string
	}{
		{
			name:        "accept",
			args:        []string{"memory", "inbox", "accept", "memory-fail"},
			memoryStub:  &memoryUsecaseStub{acceptErr: errSyntheticAcceptFailure},
			wantAction:  "accept",
			wantFailure: "synthetic accept failure",
		},
		{
			name:        "reject",
			args:        []string{"memory", "inbox", "reject", "memory-fail"},
			memoryStub:  &memoryUsecaseStub{rejectErr: errSyntheticRejectFailure},
			wantAction:  "reject",
			wantFailure: "synthetic reject failure",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(tc.memoryStub),
			)
			cmd := root.Command()
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs(append(tc.args, "--db-path", t.TempDir()+"/t.db"))
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error when %s has per-id failures", tc.wantAction)
			}
			if !strings.Contains(err.Error(), "inbox "+tc.wantAction+" failed for 1 memory id(s)") {
				t.Fatalf("unexpected error: %v", err)
			}
			out := stdout.String()
			for _, want := range []string{
				"action=" + tc.wantAction + " processed=0 failures=1",
				"FAILED\tmemory-fail\t" + tc.wantFailure,
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("stdout missing %q: %q", want, out)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr for formatted text output, got %q", stderr.String())
			}
		})
	}
}

func TestMemoryInboxBatch_JSONFailureReturnsErrorWithValidJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		args        []string
		memoryStub  *memoryUsecaseStub
		wantAction  string
		wantFailure string
	}{
		{
			name:        "accept",
			args:        []string{"memory", "inbox", "accept", "memory-fail", "--json"},
			memoryStub:  &memoryUsecaseStub{acceptErr: errSyntheticAcceptFailure},
			wantAction:  "accept",
			wantFailure: "synthetic accept failure",
		},
		{
			name:        "reject",
			args:        []string{"memory", "inbox", "reject", "memory-fail", "--json"},
			memoryStub:  &memoryUsecaseStub{rejectErr: errSyntheticRejectFailure},
			wantAction:  "reject",
			wantFailure: "synthetic reject failure",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := cli.NewRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithMemory(tc.memoryStub),
			)
			cmd := root.Command()
			stdout := &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(append(tc.args, "--db-path", t.TempDir()+"/t.db"))
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error when %s JSON output has per-id failures", tc.wantAction)
			}
			if !strings.Contains(err.Error(), "inbox "+tc.wantAction+" failed for 1 memory id(s)") {
				t.Fatalf("unexpected error: %v", err)
			}

			var payload struct {
				Action    string `json:"action"`
				Processed []any  `json:"processed"`
				Failures  []struct {
					ID    string `json:"ID"`
					Error string `json:"Error"`
				} `json:"failures"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatalf("json.Unmarshal: %v (body=%s)", err, stdout.String())
			}
			if payload.Action != tc.wantAction {
				t.Fatalf("payload.Action = %q, want %q", payload.Action, tc.wantAction)
			}
			if len(payload.Processed) != 0 {
				t.Fatalf("expected no processed rows, got %+v", payload.Processed)
			}
			if len(payload.Failures) != 1 || payload.Failures[0].ID != "memory-fail" || payload.Failures[0].Error != tc.wantFailure {
				t.Fatalf("unexpected failures payload: %+v", payload.Failures)
			}
		})
	}
}

// TestMemoryInboxAccept_TooManyPositionalArgsErrors guards the documented
// shape: positional usage is for a single id; batch use must go through
// --ids so deduplication and ordering stay deterministic.
func TestMemoryInboxAccept_TooManyPositionalArgsErrors(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "accept", "id1", "id2", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when more than one positional id is given")
	}
}

func TestMemoryInboxAccept_EmptyIDsErrors(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	)
	cmd := root.Command()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"memory", "inbox", "accept", "--db-path", t.TempDir() + "/t.db"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for empty --ids")
	}
}

func mustSummaryWithStatus(t *testing.T, id string, status domtypes.MemoryStatus) apptypes.MemorySummary {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		"fact",
		status,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		domtypes.None[time.Time](),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}
