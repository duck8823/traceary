package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_MemoryRememberCommand(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	stub := &memoryUsecaseStub{
		rememberDetails: mustMemoryDetails(t, "memory-remembered", "Remember release discipline", types.MemoryStatusAccepted),
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "remember",
		"--db-path", "/tmp/test-traceary.db",
		"--type", "decision",
		"--fact", "Remember release discipline",
		"--evidence", "issue:#462",
		"--artifact", "pr:#468",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if stub.rememberCall.memoryType != types.MemoryTypeDecision {
		t.Fatalf("memoryType = %s, want decision", stub.rememberCall.memoryType)
	}
	workspaceScope, ok := stub.rememberCall.scope.(types.WorkspaceScope)
	if !ok {
		t.Fatalf("scope type = %T, want WorkspaceScope", stub.rememberCall.scope)
	}
	if workspaceScope.Workspace().String() != "github.com/duck8823/traceary" {
		t.Fatalf("workspace = %q, want detected repo workspace", workspaceScope.Workspace().String())
	}
	if len(stub.rememberCall.evidenceRefs) != 1 || stub.rememberCall.evidenceRefs[0].Kind() != types.EvidenceRefKindIssue || stub.rememberCall.evidenceRefs[0].Value() != "#462" {
		t.Fatalf("evidenceRefs = %#v, want issue:#462", stub.rememberCall.evidenceRefs)
	}
	if len(stub.rememberCall.artifactRefs) != 1 || stub.rememberCall.artifactRefs[0].Kind() != types.ArtifactRefKindPR || stub.rememberCall.artifactRefs[0].Value() != "#468" {
		t.Fatalf("artifactRefs = %#v, want pr:#468", stub.rememberCall.artifactRefs)
	}
	if !strings.Contains(stdout.String(), "EVIDENCE_REFS:") || !strings.Contains(stdout.String(), "ARTIFACT_REFS:") {
		t.Fatalf("stdout = %q, want evidence/artifact sections", stdout.String())
	}
}

func TestRootCLI_MemoryListCommand_DefaultWorkspaceScope(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	stub := &memoryUsecaseStub{
		listResult: []apptypes.MemorySummary{mustMemorySummary(t, "memory-listed", "Listed memory", types.MemoryStatusAccepted)},
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "list", "--db-path", "/tmp/test-traceary.db", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stub.listCriteria.Scopes(); len(got) != 1 {
		t.Fatalf("len(scopes) = %d, want 1", len(got))
	} else if got[0].Kind() != types.MemoryScopeKindWorkspace || got[0].Key() != "github.com/duck8823/traceary" {
		t.Fatalf("scope = %s:%s, want workspace:github.com/duck8823/traceary", got[0].Kind(), got[0].Key())
	}
	if !strings.Contains(stdout.String(), `"memory_id": "memory-listed"`) {
		t.Fatalf("stdout = %q, want JSON summary", stdout.String())
	}
}

func TestRootCLI_MemorySearchCommand_RequiresConstraint(t *testing.T) {
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "search", "--db-path", "/tmp/test-traceary.db"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
}

func TestRootCLI_MemorySearchCommand_DoesNotLeakPositionalQuery(t *testing.T) {
	stub := &memoryUsecaseStub{
		searchResult: []apptypes.MemorySummary{mustMemorySummary(t, "memory-search", "Search memory", types.MemoryStatusAccepted)},
	}

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})

	rootCmd.SetArgs([]string{"memory", "search", "--db-path", "/tmp/test-traceary.db", "release"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if got := stub.searchCriteria.Query(); got != "release" {
		t.Fatalf("first query = %q, want release", got)
	}

	rootCmd.SetArgs([]string{"memory", "search", "--db-path", "/tmp/test-traceary.db", "--status", "accepted"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if got := stub.searchCriteria.Query(); got != "" {
		t.Fatalf("second query = %q, want empty", got)
	}
}

func TestRootCLI_MemoryShowCommand(t *testing.T) {
	details := mustMemoryDetails(t, "memory-shown", "Shown memory", types.MemoryStatusAccepted)

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(&memoryUsecaseStub{showDetails: details}),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "show", "--db-path", "/tmp/test-traceary.db", "memory-shown"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "MEMORY_ID: memory-shown") {
		t.Fatalf("stdout = %q, want memory header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "- issue:#462") || !strings.Contains(stdout.String(), "- pr:#468") {
		t.Fatalf("stdout = %q, want evidence/artifact refs", stdout.String())
	}
}

func TestRootCLI_MemoryAcceptCommand_PassesConfidence(t *testing.T) {
	stub := &memoryUsecaseStub{
		acceptDetails: mustMemoryDetails(t, "memory-accepted", "Accepted memory", types.MemoryStatusAccepted),
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "accept", "--db-path", "/tmp/test-traceary.db", "--confidence", "high", "--id-only", "memory-candidate"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.acceptCall.memoryID.String() != "memory-candidate" {
		t.Fatalf("memoryID = %q, want memory-candidate", stub.acceptCall.memoryID.String())
	}
	confidence, ok := stub.acceptCall.confidence.Value()
	if !ok || confidence != types.ConfidenceHigh {
		t.Fatalf("confidence = %v, %t, want high/true", confidence, ok)
	}
	if stdout.String() != "memory-accepted\n" {
		t.Fatalf("stdout = %q, want memory ID only", stdout.String())
	}
}

func TestRootCLI_MemoryProposeCommand_IgnoresConfidenceFlagValidation(t *testing.T) {
	stub := &memoryUsecaseStub{
		proposeDetails: mustMemoryDetails(t, "memory-proposed", "Candidate memory", types.MemoryStatusCandidate),
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "propose",
		"--db-path", "/tmp/test-traceary.db",
		"--type", "lesson",
		"--fact", "Wait for codex review before merge",
		"--confidence", "definitely-not-valid",
		"--workspace", "github.com/duck8823/traceary",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "memory-proposed") {
		t.Fatalf("stdout = %q, want proposed memory output", stdout.String())
	}
}

func TestRootCLI_MemoryExtractCommand_UsesResolvedSession(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	activeEvent, err := model.NewEvent(
		types.EventID("event-active"),
		types.EventKindSessionStarted,
		types.Client("cli"),
		types.Agent("claude"),
		types.SessionID("session-42"),
		types.Workspace("github.com/duck8823/traceary"),
		"session started",
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}

	extractionStub := &memoryUsecaseStub{
		extractDetails: []apptypes.MemoryDetails{
			mustMemoryDetails(t, "memory-extracted-1", "Always wait for Codex review before merge", types.MemoryStatusCandidate),
			mustMemoryDetails(t, "memory-extracted-2", "Decision: keep get_context raw", types.MemoryStatusCandidate),
		},
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(&sessionUsecaseStub{activeEvent: activeEvent}),
		cli.WithMemory(extractionStub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "extract",
		"--db-path", "/tmp/test-traceary.db",
		"--event-limit", "3",
		"--candidate-limit", "2",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := extractionStub.extractCriteria.SessionID().String(); got != "session-42" {
		t.Fatalf("SessionID() = %q, want session-42", got)
	}
	if got := extractionStub.extractCriteria.Workspace().String(); got != "github.com/duck8823/traceary" {
		t.Fatalf("Workspace() = %q, want github.com/duck8823/traceary", got)
	}
	if got := extractionStub.extractCriteria.EventLimit(); got != 3 {
		t.Fatalf("EventLimit() = %d, want 3", got)
	}
	if got := extractionStub.extractCriteria.CandidateLimit(); got != 2 {
		t.Fatalf("CandidateLimit() = %d, want 2", got)
	}
	if !strings.Contains(stdout.String(), "memory-extracted-1") || !strings.Contains(stdout.String(), "memory-extracted-2") {
		t.Fatalf("stdout = %q, want extracted candidate IDs", stdout.String())
	}
}

func TestRootCLI_MemoryExtractCommand_FallsBackToLatestSession(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	latestEvent, err := model.NewEvent(
		types.EventID("event-latest"),
		types.EventKindSessionStarted,
		types.Client("cli"),
		types.Agent("codex"),
		types.SessionID("session-latest"),
		types.Workspace("github.com/duck8823/traceary"),
		"session started",
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}

	sessionStub := &sessionUsecaseStub{latestEvent: latestEvent}
	extractionStub := &memoryUsecaseStub{}

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithMemory(extractionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "extract",
		"--db-path", "/tmp/test-traceary.db",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := extractionStub.extractCriteria.SessionID().String(); got != "session-latest" {
		t.Fatalf("SessionID() = %q, want session-latest", got)
	}
	if got := extractionStub.extractCriteria.Workspace().String(); got != "github.com/duck8823/traceary" {
		t.Fatalf("Workspace() = %q, want github.com/duck8823/traceary", got)
	}
	if got := sessionStub.latestCriteria.Workspace().String(); got != "github.com/duck8823/traceary" {
		t.Fatalf("Latest().Workspace() = %q, want github.com/duck8823/traceary", got)
	}
}

func TestRootCLI_MemoryExtractCommand_ExplicitSessionIDSkipsWorkspaceFilter(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "")
	cli.SetDetectRepoContextFunc(func(context.Context) (string, error) {
		return "github.com/duck8823/traceary", nil
	})
	defer cli.ResetDetectRepoContextFunc()

	sessionStub := &sessionUsecaseStub{}
	extractionStub := &memoryUsecaseStub{}

	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithSession(sessionStub),
		cli.WithMemory(extractionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "extract",
		"--db-path", "/tmp/test-traceary.db",
		"--session-id", "session-explicit",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := extractionStub.extractCriteria.SessionID().String(); got != "session-explicit" {
		t.Fatalf("SessionID() = %q, want session-explicit", got)
	}
	if got := extractionStub.extractCriteria.Workspace().String(); got != "" {
		t.Fatalf("Workspace() = %q, want empty when session-id is explicit", got)
	}
	if sessionStub.activeCriteria.Workspace().String() != "" || sessionStub.latestCriteria.Workspace().String() != "" {
		t.Fatalf("session lookup should be skipped when --session-id is explicit")
	}
}

func TestRootCLI_MemoryExtractCommand_RequiresStoreManagement(t *testing.T) {
	extractionStub := &memoryUsecaseStub{}

	rootCmd := cli.NewRootCLI(
		cli.WithMemory(extractionStub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "extract",
		"--db-path", "/tmp/test-traceary.db",
		"--session-id", "session-explicit",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want configuration error")
	}
	if !strings.Contains(err.Error(), "initialize store usecase is not configured") {
		t.Fatalf("Execute() error = %v, want store-management configuration error", err)
	}
}

func TestRootCLI_MemorySetValidityCommand_ParsesFromAndTo(t *testing.T) {
	stub := &memoryUsecaseStub{
		setValidityDetails: mustMemoryDetails(t, "memory-validity", "Memory with validity", types.MemoryStatusAccepted),
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "set-validity",
		"--db-path", "/tmp/test-traceary.db",
		"--from", "2026-04-20",
		"--to", "2026-07-01",
		"--id-only",
		"memory-validity",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.setValidityCallCount != 1 {
		t.Fatalf("setValidityCallCount = %d, want 1", stub.setValidityCallCount)
	}
	if stub.setValidityCall.memoryID.String() != "memory-validity" {
		t.Errorf("memoryID = %q, want memory-validity", stub.setValidityCall.memoryID.String())
	}
	validFrom, ok := stub.setValidityCall.validFrom.Value()
	if !ok {
		t.Fatalf("validFrom = None, want Some(2026-04-20)")
	}
	if !validFrom.Equal(time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("validFrom = %v, want 2026-04-20 UTC", validFrom)
	}
	validTo, ok := stub.setValidityCall.validTo.Value()
	if !ok {
		t.Fatalf("validTo = None, want Some(2026-07-01)")
	}
	if !validTo.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("validTo = %v, want 2026-07-01 UTC", validTo)
	}
	if stub.setValidityCall.clearTo {
		t.Errorf("clearTo = true, want false")
	}
}

func TestRootCLI_MemorySetValidityCommand_ClearToPropagates(t *testing.T) {
	stub := &memoryUsecaseStub{
		setValidityDetails: mustMemoryDetails(t, "memory-validity", "Memory with validity cleared", types.MemoryStatusAccepted),
	}

	stdout := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"memory", "set-validity",
		"--db-path", "/tmp/test-traceary.db",
		"--clear-to",
		"memory-validity",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !stub.setValidityCall.clearTo {
		t.Errorf("clearTo = false, want true")
	}
	if _, ok := stub.setValidityCall.validTo.Value(); ok {
		t.Errorf("validTo set even though only --clear-to supplied")
	}
}

func TestRootCLI_MemorySetValidityCommand_RejectsInvalidFrom(t *testing.T) {
	stub := &memoryUsecaseStub{}

	stderr := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithMemory(stub),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{
		"memory", "set-validity",
		"--db-path", "/tmp/test-traceary.db",
		"--from", "not-a-timestamp",
		"memory-validity",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want parse error")
	}
	if stub.setValidityCallCount != 0 {
		t.Errorf("setValidity invoked despite parse error; count = %d", stub.setValidityCallCount)
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("error = %v, want phrasing that identifies --from", err)
	}
}

func mustMemorySummary(t *testing.T, memoryIDValue string, fact string, status types.MemoryStatus) apptypes.MemorySummary {
	t.Helper()

	summary, err := apptypes.MemorySummaryOf(
		mustMemoryIDForCLI(t, memoryIDValue),
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("github.com/duck8823/traceary")),
		fact,
		status,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		types.None[types.MemoryID](),
		types.None[time.Time](),
		time.Date(2026, 4, 13, 9, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		time.Date(2026, 4, 13, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf() error = %v", err)
	}
	return summary
}

func mustMemoryDetails(t *testing.T, memoryIDValue string, fact string, status types.MemoryStatus) apptypes.MemoryDetails {
	t.Helper()

	memory := model.MemoryOf(
		mustMemoryIDForCLI(t, memoryIDValue),
		types.MemoryTypeDecision,
		types.WorkspaceScopeOf(types.Workspace("github.com/duck8823/traceary")),
		fact,
		status,
		types.ConfidenceVerified,
		types.MemorySourceManual,
		[]types.EvidenceRef{mustEvidenceRefForCLI(t, types.EvidenceRefKindIssue, "#462")},
		[]types.ArtifactRef{mustArtifactRefForCLI(t, types.ArtifactRefKindPR, "#468")},
		types.None[types.MemoryID](),
		types.None[time.Time](),
		time.Date(2026, 4, 13, 9, 0, 0, 0, time.UTC),
		types.None[time.Time](),
		time.Date(2026, 4, 13, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC),
	)
	details, err := apptypes.MemoryDetailsFrom(memory)
	if err != nil {
		t.Fatalf("MemoryDetailsFrom() error = %v", err)
	}
	return details
}

func mustMemoryIDForCLI(t *testing.T, value string) types.MemoryID {
	t.Helper()
	memoryID, err := types.MemoryIDFrom(value)
	if err != nil {
		t.Fatalf("MemoryIDFrom() error = %v", err)
	}
	return memoryID
}

func mustEvidenceRefForCLI(t *testing.T, kind types.EvidenceRefKind, value string) types.EvidenceRef {
	t.Helper()
	ref, err := types.EvidenceRefFrom(kind, value)
	if err != nil {
		t.Fatalf("EvidenceRefFrom() error = %v", err)
	}
	return ref
}

func mustArtifactRefForCLI(t *testing.T, kind types.ArtifactRefKind, value string) types.ArtifactRef {
	t.Helper()
	ref, err := types.ArtifactRefFrom(kind, value)
	if err != nil {
		t.Fatalf("ArtifactRefFrom() error = %v", err)
	}
	return ref
}
