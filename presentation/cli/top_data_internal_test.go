package cli

import (
	"context"
	"errors"
	"strings"
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
	showDetails  apptypes.EventDetails
	showErr      error
	showEventID  domtypes.EventID
	showCalls    int
}

func (s *topDataEventStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listEvents, s.listErr
}

func (s *topDataEventStub) Show(_ context.Context, eventID domtypes.EventID) (apptypes.EventDetails, error) {
	s.showEventID = eventID
	s.showCalls++
	return s.showDetails, s.showErr
}

// topDataMemoryStub satisfies usecase.MemoryUsecase via the embedded
// interface trick. Only List is exercised by topDataLoader.
type topDataMemoryStub struct {
	usecase.MemoryUsecase

	listResult          []apptypes.MemorySummary
	listErr             error
	listCriteria        apptypes.MemoryListCriteria
	listCalls           int
	staleResult         apptypes.StaleMemoryListResult
	staleErr            error
	staleMemoryCriteria apptypes.StaleMemoryListCriteria
	staleMemoryCalls    int
	showDetails         apptypes.MemoryDetails
	showErr             error
	showMemoryID        domtypes.MemoryID
	showCalls           int
}

func (s *topDataMemoryStub) List(_ context.Context, criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
	s.listCriteria = criteria
	s.listCalls++
	return s.listResult, s.listErr
}

func (s *topDataMemoryStub) ListStale(_ context.Context, criteria apptypes.StaleMemoryListCriteria) (apptypes.StaleMemoryListResult, error) {
	s.staleMemoryCriteria = criteria
	s.staleMemoryCalls++
	return s.staleResult, s.staleErr
}

func (s *topDataMemoryStub) Show(_ context.Context, memoryID domtypes.MemoryID) (apptypes.MemoryDetails, error) {
	s.showMemoryID = memoryID
	s.showCalls++
	return s.showDetails, s.showErr
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

func TestTopDataLoader_LoadStaleMemories_ForwardsScopesAndLimit(t *testing.T) {
	t.Parallel()

	staleSummary := memorySummaryFixture(t, "mem-stale", domtypes.MemoryStatusSuperseded, "stale fixture")
	staleRow, err := apptypes.StaleMemoryRowOf(staleSummary, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(7, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}
	memory := &topDataMemoryStub{staleResult: staleResult}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadStaleMemories(context.Background(), topDataCriteria{
		Workspace:        "  duck8823/traceary  ",
		Client:           "claude",
		Agent:            "  codex  ",
		StaleMemoryLimit: 4,
	})
	if err != nil {
		t.Fatalf("loadStaleMemories() error = %v", err)
	}
	if got.Count() != 7 {
		t.Fatalf("loadStaleMemories().Count = %d, want 7", got.Count())
	}
	if items := got.Items(); len(items) != 1 || items[0].Summary().MemoryID().String() != "mem-stale" {
		t.Fatalf("loadStaleMemories().Items = %#v, want one mem-stale", items)
	}
	if got, want := memory.staleMemoryCriteria.Limit(), 4; got != want {
		t.Fatalf("ListStale criteria Limit = %d, want %d", got, want)
	}
	scopes := memory.staleMemoryCriteria.Scopes()
	if len(scopes) != 2 {
		t.Fatalf("ListStale criteria Scopes length = %d, want 2 (workspace + agent)", len(scopes))
	}
	workspaceScope, ok := scopes[0].(domtypes.WorkspaceScope)
	if !ok {
		t.Fatalf("scopes[0] = %T, want WorkspaceScope", scopes[0])
	}
	if got, want := workspaceScope.Workspace().String(), "duck8823/traceary"; got != want {
		t.Fatalf("scopes[0].Workspace = %q, want %q", got, want)
	}
	agentScope, ok := scopes[1].(domtypes.AgentScope)
	if !ok {
		t.Fatalf("scopes[1] = %T, want AgentScope", scopes[1])
	}
	if got, want := agentScope.Agent().String(), "codex"; got != want {
		t.Fatalf("scopes[1].Agent = %q, want %q", got, want)
	}
}

func TestTopDataLoader_LoadStaleMemories_ZeroLimitIsNoOp(t *testing.T) {
	t.Parallel()

	memory := &topDataMemoryStub{}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadStaleMemories(context.Background(), topDataCriteria{StaleMemoryLimit: 0})
	if err != nil {
		t.Fatalf("loadStaleMemories() error = %v", err)
	}
	if got.Count() != 0 || len(got.Items()) != 0 {
		t.Fatalf("loadStaleMemories() = %#v, want empty result when StaleMemoryLimit <= 0", got)
	}
	if memory.staleMemoryCalls != 0 {
		t.Fatalf("memory.ListStale calls = %d, want 0", memory.staleMemoryCalls)
	}
}

func TestTopDataLoader_LoadDetail_SessionUsesLineageAndRecentEvents(t *testing.T) {
	t.Parallel()

	root := sessionSummaryFixture("root", "", fixedStartedAt, "ended", domtypes.EventKindSessionEnded, "root ended")
	child := sessionSummaryFixture("child", "root", fixedStartedAt.Add(10*time.Minute), "active", domtypes.EventKindTranscript, "child active")
	event := mustEvent(t, "evt-session", domtypes.EventKindTranscript, "first session event line\nsecond session event line")
	session := &topDataSessionStub{lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
		domtypes.SessionID("child"): {root, child},
	}}
	events := &topDataEventStub{listEvents: []*model.Event{event}}
	loader := newTopDataLoader(session, events, nil)

	got, err := loader.loadDetail(context.Background(), topDetailRequest{
		target: topDetailTarget{kind: topDetailSession, title: "SESSION child", sessionID: domtypes.SessionID("child")},
	})
	if err != nil {
		t.Fatalf("loadDetail(session) error = %v", err)
	}
	joined := strings.Join(got.lines, "\n")
	for _, expect := range []string{"SESSION_ID: child", "LINEAGE:", "root", "child", "RECENT_EVENTS", "first session event line", "  second session event line"} {
		if !strings.Contains(joined, expect) {
			t.Fatalf("session detail missing %q:\n%s", expect, joined)
		}
	}
	for _, line := range got.lines {
		if strings.Contains(line, "first session event line") && strings.Contains(line, "second session event line") {
			t.Fatalf("multiline event body should be split into modal rows, got combined line %q", line)
		}
	}
	if len(session.lineageCalls) != 1 || session.lineageCalls[0] != "child" {
		t.Fatalf("Lineage calls = %#v, want child", session.lineageCalls)
	}
	if events.listCriteria.SessionID() != "child" || events.listCriteria.Limit() != topDetailRecentEventLimit {
		t.Fatalf("event List criteria = session %q limit %d, want child/%d", events.listCriteria.SessionID(), events.listCriteria.Limit(), topDetailRecentEventLimit)
	}
}

func TestTopDataLoader_LoadDetail_EventUsesShowAndFormatsAudit(t *testing.T) {
	t.Parallel()

	event := mustEvent(t, "evt-detail", domtypes.EventKindCommandExecuted, "full event body")
	audit := model.CommandAuditOf(
		event.EventID(),
		"go test ./...",
		"stdin payload",
		"stdout payload",
		false,
		false,
		domtypes.Some(1),
	)
	details, err := apptypes.EventDetailsOf(event, domtypes.Some(audit))
	if err != nil {
		t.Fatalf("EventDetailsOf: %v", err)
	}
	events := &topDataEventStub{showDetails: details}
	loader := newTopDataLoader(nil, events, nil)

	got, err := loader.loadDetail(context.Background(), topDetailRequest{
		target: topDetailTarget{kind: topDetailEvent, title: "EVENT evt-detail", eventID: event.EventID()},
	})
	if err != nil {
		t.Fatalf("loadDetail(event) error = %v", err)
	}
	joined := strings.Join(got.lines, "\n")
	for _, expect := range []string{"EVENT_ID: evt-detail", "MESSAGE: full event body", "COMMAND: go test ./...", "EXIT_CODE: 1", "stdout payload"} {
		if !strings.Contains(joined, expect) {
			t.Fatalf("event detail missing %q:\n%s", expect, joined)
		}
	}
	if events.showCalls != 1 || events.showEventID != "evt-detail" {
		t.Fatalf("Show calls/id = %d/%q, want 1/evt-detail", events.showCalls, events.showEventID)
	}
}

func TestTopDataLoader_LoadDetail_MemoryUsesShowAndFormatsRefs(t *testing.T) {
	t.Parallel()

	summary := memorySummaryFixture(t, "mem-detail", domtypes.MemoryStatusCandidate, "full memory fact")
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindEvent, "evt-detail")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	artifact, err := domtypes.ArtifactRefFrom(domtypes.ArtifactRefKindFile, "docs/plan.md")
	if err != nil {
		t.Fatalf("ArtifactRefFrom: %v", err)
	}
	details := apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{evidence}, []domtypes.ArtifactRef{artifact})
	memory := &topDataMemoryStub{showDetails: details}
	loader := newTopDataLoader(nil, nil, memory)

	got, err := loader.loadDetail(context.Background(), topDetailRequest{
		target: topDetailTarget{kind: topDetailMemory, title: "MEMORY mem-detail", memoryID: summary.MemoryID()},
	})
	if err != nil {
		t.Fatalf("loadDetail(memory) error = %v", err)
	}
	joined := strings.Join(got.lines, "\n")
	for _, expect := range []string{"MEMORY_ID: mem-detail", "FACT: full memory fact", "EVIDENCE_REFS:", "event:evt-detail", "ARTIFACT_REFS:", "file:docs/plan.md"} {
		if !strings.Contains(joined, expect) {
			t.Fatalf("memory detail missing %q:\n%s", expect, joined)
		}
	}
	if memory.showCalls != 1 || memory.showMemoryID != "mem-detail" {
		t.Fatalf("Show calls/id = %d/%q, want 1/mem-detail", memory.showCalls, memory.showMemoryID)
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
	staleSummary := memorySummaryFixture(t, "mem-stale", domtypes.MemoryStatusSuperseded, "stale snapshot fixture")
	staleRow, err := apptypes.StaleMemoryRowOf(staleSummary, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(3, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}

	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{root},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("root"): {root},
		},
	}
	memory := &topDataMemoryStub{listResult: []apptypes.MemorySummary{candidate}, staleResult: staleResult}
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
		StaleMemoryLimit:   2,
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
	if snap.StaleMemories.Count() != 3 {
		t.Fatalf("snap.StaleMemories.Count = %d, want 3", snap.StaleMemories.Count())
	}
	if items := snap.StaleMemories.Items(); len(items) != 1 || items[0].Summary().MemoryID().String() != "mem-stale" {
		t.Fatalf("snap.StaleMemories.Items = %#v, want one mem-stale", items)
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
		StaleMemoryLimit:   10,
	})
	if err != nil {
		t.Fatalf("loadSnapshot() error = %v", err)
	}
	if snap.Sessions != nil || snap.Failures != nil || snap.RecentCommands != nil || snap.Candidates != nil || snap.StaleMemories.Count() != 0 || len(snap.StaleMemories.Items()) != 0 {
		t.Fatalf("loadSnapshot() = %#v, want zero-value snapshot when no usecases are wired", snap)
	}
}

func memorySummaryFixture(t *testing.T, id string, status domtypes.MemoryStatus, fact string) apptypes.MemorySummary {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		status,
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
	return summary
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
