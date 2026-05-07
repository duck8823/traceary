package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// topDataSessionStub satisfies usecase.SessionUsecase by embedding the
// interface and overriding only the methods topDataLoader exercises.
// Calling any other method panics, which is exactly what we want from a
// test seam: it keeps the stub small while making accidental callers
// fail loudly.
type topDataSessionStub struct {
	usecase.SessionUsecase

	listResult   []apptypes.SessionSummary
	listErr      error
	listCriteria apptypes.SessionListCriteria
	listCalls    int

	lineageByID  map[domtypes.SessionID][]apptypes.SessionSummary
	lineageErr   error
	lineageCalls []domtypes.SessionID
}

func (s *topDataSessionStub) List(_ context.Context, criteria apptypes.SessionListCriteria) ([]apptypes.SessionSummary, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listResult, s.listErr
}

func (s *topDataSessionStub) Lineage(_ context.Context, id domtypes.SessionID) ([]apptypes.SessionSummary, error) {
	s.lineageCalls = append(s.lineageCalls, id)
	if s.lineageErr != nil {
		return nil, s.lineageErr
	}
	if lineage, ok := s.lineageByID[id]; ok {
		return lineage, nil
	}
	return nil, nil
}

// topDataEventStub satisfies usecase.EventUsecase via the same embedded
// interface trick. Only List is exercised by topDataLoader.
type topDataEventStub struct {
	usecase.EventUsecase

	listEvents   []*model.Event
	listErr      error
	listCriteria apptypes.EventListCriteria
	listCalls    int
}

func (s *topDataEventStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listEvents, s.listErr
}

// topDataMemoryStub satisfies usecase.MemoryUsecase via the embedded
// interface trick. Only List is exercised by topDataLoader.
type topDataMemoryStub struct {
	usecase.MemoryUsecase

	listResult   []apptypes.MemorySummary
	listErr      error
	listCriteria apptypes.MemoryListCriteria
	listCalls    int
}

func (s *topDataMemoryStub) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listResult, s.listErr
}

// fixedStartedAt is the deterministic anchor every fixture in this file
// derives session/event timestamps from. Pinning a single instant keeps
// table-driven assertions readable without `time.Now()` drift.
var fixedStartedAt = time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

func sessionSummaryFixture(id string, parent string, started time.Time, status string, latestKind domtypes.EventKind, latestMessage string) apptypes.SessionSummary {
	endedAt := domtypes.None[time.Time]()
	if status == "ended" {
		endedAt = domtypes.Some(started.Add(30 * time.Minute))
	}
	return apptypes.SessionSummaryOf(
		domtypes.SessionID(id),
		domtypes.Workspace("duck8823/traceary"),
		started,
		endedAt,
		status,
		7,
		2,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(parent),
		domtypes.Client("claude"),
		started.Add(5*time.Minute),
		apptypes.SessionSummaryLatestEventOf(latestKind, latestMessage),
	)
}

func TestTopDataLoader_LoadSessions_BuildsActiveTreeAndForwardsCriteria(t *testing.T) {
	t.Parallel()

	root := sessionSummaryFixture("root", "", fixedStartedAt, "ended", domtypes.EventKindSessionEnded, "root ended")
	child := sessionSummaryFixture("child", "root", fixedStartedAt.Add(10*time.Minute), "active", domtypes.EventKindTranscript, "active child")

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{child},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("child"): {root, child},
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{
		Workspace:    "duck8823/traceary",
		Client:       "claude",
		Agent:        "claude",
		SessionLimit: 50,
	})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}

	if got, want := session.listCriteria.Limit(), 50; got != want {
		t.Fatalf("List criteria Limit = %d, want %d", got, want)
	}
	if !session.listCriteria.ActiveOnly() {
		t.Fatalf("List criteria ActiveOnly = false, want true")
	}
	if got, want := session.listCriteria.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("List criteria Workspace = %q, want %q", got, want)
	}
	if got, want := session.listCriteria.Client().String(), "claude"; got != want {
		t.Fatalf("List criteria Client = %q, want %q", got, want)
	}
	if got, want := session.listCriteria.Agent().String(), "claude"; got != want {
		t.Fatalf("List criteria Agent = %q, want %q", got, want)
	}

	if len(roots) != 1 {
		t.Fatalf("roots length = %d, want 1 (ended root retained because the active child references it)", len(roots))
	}
	if got, want := roots[0].summary.SessionID().String(), "root"; got != want {
		t.Fatalf("root[0].SessionID = %q, want %q", got, want)
	}
	if len(roots[0].children) != 1 {
		t.Fatalf("root[0].children length = %d, want 1", len(roots[0].children))
	}
	if got, want := roots[0].children[0].summary.SessionID().String(), "child"; got != want {
		t.Fatalf("root[0].children[0].SessionID = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadSessions_NonPositiveLimitIsNoOp(t *testing.T) {
	t.Parallel()

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{
			sessionSummaryFixture("only", "", fixedStartedAt, "active", domtypes.EventKindNote, "noop"),
		},
	}
	loader := newTopDataLoader(session, nil, nil)

	roots, err := loader.loadSessions(context.Background(), topDataCriteria{SessionLimit: 0})
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}
	if roots != nil {
		t.Fatalf("roots = %v, want nil when SessionLimit <= 0", roots)
	}
	if session.listCalls != 0 {
		t.Fatalf("session.List calls = %d, want 0", session.listCalls)
	}
}

func TestTopDataLoader_LoadSessions_ListErrorWrapsLocalizedMessage(t *testing.T) {
	t.Parallel()

	session := &topDataSessionStub{listErr: errors.New("boom")}
	loader := newTopDataLoader(session, nil, nil)

	_, err := loader.loadSessions(context.Background(), topDataCriteria{SessionLimit: 10})
	if err == nil {
		t.Fatalf("loadSessions() error = nil, want wrapped list error")
	}
	if !errors.Is(err, session.listErr) {
		t.Fatalf("loadSessions() error chain missing underlying boom: %v", err)
	}
}

func TestTopDataLoader_LoadFailures_BuildsCriteriaAndReturnsEvents(t *testing.T) {
	t.Parallel()

	failure := mustEvent(t, "evt-fail", domtypes.EventKindCommandExecuted, "go test ./... [exit=1]")
	event := &topDataEventStub{listEvents: []*model.Event{failure}}
	loader := newTopDataLoader(nil, event, nil)

	got, err := loader.loadFailures(context.Background(), topDataCriteria{
		Workspace:    "duck8823/traceary",
		Client:       "claude",
		Agent:        "claude",
		FailureLimit: 5,
	})
	if err != nil {
		t.Fatalf("loadFailures() error = %v", err)
	}
	if len(got) != 1 || got[0].EventID().String() != "evt-fail" {
		t.Fatalf("loadFailures() = %#v, want one evt-fail event", got)
	}

	if got, want := event.listCriteria.Limit(), 5; got != want {
		t.Fatalf("List criteria Limit = %d, want %d", got, want)
	}
	if !event.listCriteria.FailuresOnly() {
		t.Fatalf("List criteria FailuresOnly = false, want true")
	}
	if got, want := event.listCriteria.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("List criteria Workspace = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadFailures_ZeroLimitIsNoOp(t *testing.T) {
	t.Parallel()

	event := &topDataEventStub{}
	loader := newTopDataLoader(nil, event, nil)

	got, err := loader.loadFailures(context.Background(), topDataCriteria{FailureLimit: 0})
	if err != nil {
		t.Fatalf("loadFailures() error = %v", err)
	}
	if got != nil {
		t.Fatalf("loadFailures() = %v, want nil when FailureLimit <= 0", got)
	}
	if event.listCalls != 0 {
		t.Fatalf("event.List calls = %d, want 0", event.listCalls)
	}
}

func TestTopDataLoader_LoadRecentCommands_FiltersByCommandKind(t *testing.T) {
	t.Parallel()

	cmd := mustEvent(t, "evt-cmd", domtypes.EventKindCommandExecuted, "ls -la")
	event := &topDataEventStub{listEvents: []*model.Event{cmd}}
	loader := newTopDataLoader(nil, event, nil)

	got, err := loader.loadRecentCommands(context.Background(), topDataCriteria{RecentCommandLimit: 3})
	if err != nil {
		t.Fatalf("loadRecentCommands() error = %v", err)
	}
	if len(got) != 1 || got[0].EventID().String() != "evt-cmd" {
		t.Fatalf("loadRecentCommands() = %#v, want one evt-cmd event", got)
	}

	if got, want := event.listCriteria.Kind(), domtypes.EventKindCommandExecuted; got != want {
		t.Fatalf("List criteria Kind = %q, want %q", got, want)
	}
	if event.listCriteria.FailuresOnly() {
		t.Fatalf("List criteria FailuresOnly = true, want false (recent commands include successes)")
	}
}

func TestTopDataLoader_LoadCandidates_FiltersByCandidateStatus(t *testing.T) {
	t.Parallel()

	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	candidate, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("mem-1"),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		"prefer table-driven subtests",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedStartedAt,
		domtypes.None[time.Time](),
		fixedStartedAt,
		fixedStartedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	memory := &topDataMemoryStub{listResult: []apptypes.MemorySummary{candidate}}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadCandidates(context.Background(), topDataCriteria{CandidateLimit: 4})
	if err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}
	if len(got) != 1 || got[0].MemoryID().String() != "mem-1" {
		t.Fatalf("loadCandidates() = %#v, want one mem-1 candidate", got)
	}

	statuses := memory.listCriteria.Statuses()
	if len(statuses) != 1 || statuses[0] != domtypes.MemoryStatusCandidate {
		t.Fatalf("List criteria Statuses = %v, want [candidate]", statuses)
	}
	if !memory.listCriteria.RememberIntentPriority() {
		t.Fatalf("List criteria RememberIntentPriority = false, want true (matches inbox ordering)")
	}
	if got, want := memory.listCriteria.Limit(), 4; got != want {
		t.Fatalf("List criteria Limit = %d, want %d", got, want)
	}
	if scopes := memory.listCriteria.Scopes(); len(scopes) != 0 {
		t.Fatalf("List criteria Scopes = %v, want none when Workspace/Agent are unset", scopes)
	}
}

func TestTopDataLoader_LoadCandidates_WorkspaceAndAgentAddScopes(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Workspace:      "  duck8823/traceary  ",
		Client:         "claude",
		Agent:          "  claude  ",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	scopes := memory.listCriteria.Scopes()
	if len(scopes) != 2 {
		t.Fatalf("List criteria Scopes length = %d, want 2 (workspace + agent)", len(scopes))
	}
	workspaceScope, ok := scopes[0].(domtypes.WorkspaceScope)
	if !ok {
		t.Fatalf("scopes[0] = %T, want WorkspaceScope", scopes[0])
	}
	if got, want := workspaceScope.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("scopes[0].Workspace = %q, want %q (trimmed)", got, want)
	}
	agentScope, ok := scopes[1].(domtypes.AgentScope)
	if !ok {
		t.Fatalf("scopes[1] = %T, want AgentScope", scopes[1])
	}
	if got, want := agentScope.Agent().String(), "claude"; got != want {
		t.Fatalf("scopes[1].Agent = %q, want %q (trimmed)", got, want)
	}
	statuses := memory.listCriteria.Statuses()
	if len(statuses) != 1 || statuses[0] != domtypes.MemoryStatusCandidate {
		t.Fatalf("List criteria Statuses = %v, want [candidate] when scoped", statuses)
	}
	if !memory.listCriteria.RememberIntentPriority() {
		t.Fatalf("List criteria RememberIntentPriority = false, want true when scoped")
	}
}

func TestTopDataLoader_LoadCandidates_WorkspaceOnlyScope(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Workspace:      "duck8823/traceary",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	scopes := memory.listCriteria.Scopes()
	if len(scopes) != 1 {
		t.Fatalf("List criteria Scopes length = %d, want 1 (workspace only)", len(scopes))
	}
	if _, ok := scopes[0].(domtypes.WorkspaceScope); !ok {
		t.Fatalf("scopes[0] = %T, want WorkspaceScope", scopes[0])
	}
}

func TestTopDataLoader_LoadCandidates_AgentOnlyScope(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Agent:          "claude",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	scopes := memory.listCriteria.Scopes()
	if len(scopes) != 1 {
		t.Fatalf("List criteria Scopes length = %d, want 1 (agent only)", len(scopes))
	}
	if _, ok := scopes[0].(domtypes.AgentScope); !ok {
		t.Fatalf("scopes[0] = %T, want AgentScope", scopes[0])
	}
}

func TestTopDataLoader_LoadCandidates_ClientDoesNotAddScope(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	if _, err := loader.loadCandidates(context.Background(), topDataCriteria{
		Client:         "claude",
		CandidateLimit: 4,
	}); err != nil {
		t.Fatalf("loadCandidates() error = %v", err)
	}

	if scopes := memory.listCriteria.Scopes(); len(scopes) != 0 {
		t.Fatalf("List criteria Scopes = %v, want none when only Client is set (client has no memory scope equivalent)", scopes)
	}
}

func TestTopDataLoader_LoadSnapshot_AggregatesEveryPane(t *testing.T) {
	t.Parallel()

	root := sessionSummaryFixture("root", "", fixedStartedAt, "active", domtypes.EventKindTranscript, "active root")
	failure := mustEvent(t, "evt-fail", domtypes.EventKindCommandExecuted, "go test ./... [exit=1]")
	cmd := mustEvent(t, "evt-cmd", domtypes.EventKindCommandExecuted, "ls -la")
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	candidate, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID("mem-1"),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		"snapshot fixture",
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedStartedAt,
		domtypes.None[time.Time](),
		fixedStartedAt,
		fixedStartedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{root},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("root"): {root},
		},
	}
	memory := &topDataMemoryStub{listResult: []apptypes.MemorySummary{candidate}}
	// snapshotEventStub differentiates the two EventUsecase.List calls
	// loadSnapshot makes (failures vs recent commands) by inspecting
	// the FailuresOnly / Kind criteria, so each pane sees a distinct
	// fixture.
	event := &snapshotEventStub{failures: []*model.Event{failure}, commands: []*model.Event{cmd}}
	loader := newTopDataLoader(session, event, memory)

	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		SessionLimit:       10,
		FailureLimit:       3,
		RecentCommandLimit: 3,
		CandidateLimit:     2,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].summary.SessionID().String() != "root" {
		t.Fatalf("snap.Sessions = %#v, want one root", snap.Sessions)
	}
	if len(snap.Failures) != 1 || snap.Failures[0].EventID().String() != "evt-fail" {
		t.Fatalf("snap.Failures = %#v, want one evt-fail", snap.Failures)
	}
	if len(snap.RecentCommands) != 1 || snap.RecentCommands[0].EventID().String() != "evt-cmd" {
		t.Fatalf("snap.RecentCommands = %#v, want one evt-cmd", snap.RecentCommands)
	}
	if len(snap.Candidates) != 1 || snap.Candidates[0].MemoryID().String() != "mem-1" {
		t.Fatalf("snap.Candidates = %#v, want one mem-1", snap.Candidates)
	}
}

// snapshotEventStub returns separate fixtures for the failures call and
// the recent-commands call so loadSnapshot's two EventUsecase.List
// invocations can be distinguished by the FailuresOnly / Kind criteria.
type snapshotEventStub struct {
	usecase.EventUsecase

	failures []*model.Event
	commands []*model.Event
}

func (s *snapshotEventStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	if criteria.FailuresOnly() {
		return s.failures, nil
	}
	if criteria.Kind() == domtypes.EventKindCommandExecuted {
		return s.commands, nil
	}
	return nil, nil
}

func TestTopDataLoader_LoadSnapshot_NoUsecasesReturnsEmpty(t *testing.T) {
	t.Parallel()

	loader := newTopDataLoader(nil, nil, nil)
	snap, err := loader.loadSnapshot(context.Background(), topDataCriteria{
		SessionLimit:       10,
		FailureLimit:       10,
		RecentCommandLimit: 10,
		CandidateLimit:     10,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if snap.Sessions != nil || snap.Failures != nil || snap.RecentCommands != nil || snap.Candidates != nil {
		t.Fatalf("loadSnapshot() = %#v, want zero-value snapshot when no usecases are wired", snap)
	}
}

func mustEvent(t *testing.T, id string, kind domtypes.EventKind, body string) *model.Event {
	t.Helper()
	return model.EventOf(
		domtypes.EventID(id),
		kind,
		domtypes.Client("claude"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("duck8823/traceary"),
		body,
		fixedStartedAt,
	)
}
