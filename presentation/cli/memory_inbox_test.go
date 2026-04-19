package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func buildInboxCandidateDetails(t *testing.T, id string, fact string, source domtypes.MemorySource) apptypes.MemoryDetails {
	t.Helper()
	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
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
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	evidence, err := domtypes.EvidenceRefOf(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L2")
	if err != nil {
		t.Fatalf("EvidenceRefOf: %v", err)
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
	workspace, err := domtypes.WorkspaceOf("github.com/example/repo")
	if err != nil {
		t.Fatalf("WorkspaceOf: %v", err)
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
		time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}
