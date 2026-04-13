package cli

import (
	"bytes"
	"context"
	"strconv"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type tailEventUsecaseStub struct {
	listResponses [][]*model.Event
	listErrs      []error
	listCalls     []apptypes.EventListCriteria
	onList        func(callIndex int, criteria apptypes.EventListCriteria)
}

func (s *tailEventUsecaseStub) Log(context.Context, string, types.EventKind, types.Client, types.Agent, types.SessionID, types.Workspace) (*model.Event, error) {
	return nil, nil
}

func (s *tailEventUsecaseStub) Audit(context.Context, string, string, string, types.Client, types.Agent, types.SessionID, types.Workspace, types.Optional[int], apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	return nil, nil, nil
}

func (s *tailEventUsecaseStub) Search(context.Context, apptypes.EventSearchCriteria) ([]*model.Event, error) {
	return nil, nil
}

func (s *tailEventUsecaseStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	callIndex := len(s.listCalls)
	s.listCalls = append(s.listCalls, criteria)
	if s.onList != nil {
		s.onList(callIndex, criteria)
	}
	if callIndex < len(s.listErrs) && s.listErrs[callIndex] != nil {
		return nil, s.listErrs[callIndex]
	}
	if callIndex < len(s.listResponses) {
		return s.listResponses[callIndex], nil
	}
	return nil, nil
}

func (s *tailEventUsecaseStub) Show(context.Context, types.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}

func (s *tailEventUsecaseStub) Context(context.Context, apptypes.EventContextCriteria) ([]*model.Event, error) {
	return nil, nil
}

func (s *tailEventUsecaseStub) Timeline(context.Context, apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}

type tailStoreManagementStub struct{}

func (tailStoreManagementStub) Initialize(context.Context) error { return nil }
func (tailStoreManagementStub) CreateBackup(context.Context, string, bool) error {
	return nil
}
func (tailStoreManagementStub) RestoreBackup(context.Context, string, bool) error {
	return nil
}
func (tailStoreManagementStub) CollectGarbage(context.Context, time.Time, bool) (apptypes.CollectGarbageResult, error) {
	return apptypes.CollectGarbageResult{}, nil
}
func (tailStoreManagementStub) CloseStaleSessions(context.Context, time.Duration, bool) (apptypes.CloseStaleSessionsResult, error) {
	return apptypes.CloseStaleSessionsResult{}, nil
}

type fakeTailTicker struct {
	ch chan time.Time
}

func newFakeTailTicker() *fakeTailTicker {
	return &fakeTailTicker{ch: make(chan time.Time, 4)}
}

func (t *fakeTailTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTailTicker) Stop()               {}

func TestRunTail_PrintsInitialEventsAndFollowsNewEvents(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	ticker := newFakeTailTicker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialOlder := mustTailEvent(t, "event-1", "cli", "codex", "session-1", "duck8823/traceary", "older", time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC))
	initialNewer := mustTailEvent(t, "event-2", "cli", "codex", "session-1", "duck8823/traceary", "newer", time.Date(2026, 4, 13, 18, 5, 0, 0, time.UTC))
	followup := mustTailEvent(t, "event-3", "cli", "codex", "session-1", "duck8823/traceary", "latest", time.Date(2026, 4, 13, 18, 10, 0, 0, time.UTC))

	firstListDone := make(chan struct{})
	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{
			{initialNewer, initialOlder},
			{followup, initialNewer},
		},
		onList: func(callIndex int, criteria apptypes.EventListCriteria) {
			switch callIndex {
			case 0:
				close(firstListDone)
			case 1:
				cancel()
				if got := criteria.From(); !got.Equal(initialNewer.CreatedAt()) {
					t.Fatalf("criteria.From() = %v, want %v", got, initialNewer.CreatedAt())
				}
			}
		},
	}

	stdout := &bytes.Buffer{}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- sut.runTail(ctx, stdout, tailCommandInput{
			dbPath:        "/tmp/test-traceary.db",
			limit:         2,
			client:        "cli",
			agent:         "codex",
			sessionID:     "session-1",
			repo:          "duck8823/traceary",
			nowFunc:       func() time.Time { return startTime },
			tickerFactory: func(time.Duration) tailTicker { return ticker },
		})
	}()

	<-firstListDone
	ticker.ch <- time.Now()

	if err := <-errCh; err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tMESSAGE\n" +
		"2026-04-13T18:00:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\tolder\n" +
		"2026-04-13T18:05:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\tnewer\n" +
		"2026-04-13T18:10:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\tlatest\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunTail_ZeroLimitStartsFromNow(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 4, 13, 19, 30, 0, 0, time.UTC)
	ticker := newFakeTailTicker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	older := mustTailEvent(t, "event-older", "cli", "codex", "session-1", "duck8823/traceary", "older", time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC))
	newer := mustTailEvent(t, "event-newer", "cli", "codex", "session-1", "duck8823/traceary", "new", time.Date(2026, 4, 13, 19, 45, 0, 0, time.UTC))

	firstPoll := make(chan struct{})
	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{
			{newer, older},
		},
		onList: func(callIndex int, _ apptypes.EventListCriteria) {
			if callIndex == 0 {
				close(firstPoll)
				cancel()
			}
		},
	}
	stdout := &bytes.Buffer{}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- sut.runTail(ctx, stdout, tailCommandInput{
			dbPath:        "/tmp/test-traceary.db",
			limit:         0,
			repo:          "duck8823/traceary",
			nowFunc:       func() time.Time { return startTime },
			tickerFactory: func(time.Duration) tailTicker { return ticker },
		})
	}()

	ticker.ch <- time.Now()
	<-firstPoll

	if err := <-errCh; err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tMESSAGE\n" +
		"2026-04-13T19:45:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\tnew\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunTail_JSONOutputsNDJSON(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	event := mustTailEvent(t, "event-json", "cli", "codex", "session-2", "duck8823/traceary", "hello json", time.Date(2026, 4, 13, 19, 55, 0, 0, time.UTC))
	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{{event}},
		onList: func(callIndex int, _ apptypes.EventListCriteria) {
			if callIndex == 0 {
				cancel()
			}
		},
	}
	stdout := &bytes.Buffer{}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	if err := sut.runTail(ctx, stdout, tailCommandInput{
		dbPath:        "/tmp/test-traceary.db",
		limit:         1,
		repo:          "duck8823/traceary",
		asJSON:        true,
		nowFunc:       func() time.Time { return time.Date(2026, 4, 13, 20, 0, 0, 0, time.UTC) },
		tickerFactory: func(time.Duration) tailTicker { return newFakeTailTicker() },
	}); err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	want := "{\"event_id\":\"event-json\",\"kind\":\"note\",\"client\":\"cli\",\"agent\":\"codex\",\"session_id\":\"session-2\",\"workspace\":\"duck8823/traceary\",\"message\":\"hello json\",\"created_at\":\"2026-04-13T19:55:00Z\"}\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestListTailEventsSince_DeduplicatesSeenIDsAtSameTimestamp(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2026, 4, 13, 18, 5, 0, 0, time.UTC)
	seen := mustTailEvent(t, "event-seen", "cli", "codex", "session-1", "duck8823/traceary", "seen", timestamp)
	fresh := mustTailEvent(t, "event-fresh", "cli", "codex", "session-1", "duck8823/traceary", "fresh", timestamp)

	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{
			{fresh, seen},
		},
	}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	cursor := newTailCursor(timestamp)
	cursor.seenIDs[seen.EventID().String()] = struct{}{}

	events, err := sut.listTailEventsSince(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).Build(),
		cursor,
		timestamp.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("listTailEventsSince() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if got := events[0].EventID().String(); got != fresh.EventID().String() {
		t.Fatalf("EventID() = %q, want %q", got, fresh.EventID().String())
	}
}

func TestListTailEventsSince_UsesStableSnapshotAcrossPages(t *testing.T) {
	t.Parallel()

	cursorTime := time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC)
	snapshotTo := time.Date(2026, 4, 13, 18, 10, 0, 0, time.UTC)

	firstPage := make([]*model.Event, 0, defaultTailBatchSize)
	secondPage := make([]*model.Event, 0, defaultTailBatchSize)
	for i := range defaultTailBatchSize {
		firstPage = append(firstPage, mustTailEvent(
			t,
			"event-first-"+strconv.Itoa(i),
			"cli",
			"codex",
			"session-1",
			"duck8823/traceary",
			"first",
			cursorTime.Add(time.Duration(defaultTailBatchSize-i)*time.Second),
		))
		secondPage = append(secondPage, mustTailEvent(
			t,
			"event-second-"+strconv.Itoa(i),
			"cli",
			"codex",
			"session-1",
			"duck8823/traceary",
			"second",
			cursorTime.Add(time.Duration(defaultTailBatchSize*2-i)*time.Second),
		))
	}
	lastPage := []*model.Event{
		mustTailEvent(t, "event-last", "cli", "codex", "session-1", "duck8823/traceary", "last", cursorTime.Add(5*time.Minute)),
	}

	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{firstPage, secondPage, lastPage},
		onList: func(callIndex int, criteria apptypes.EventListCriteria) {
			if !criteria.To().Equal(snapshotTo) {
				t.Fatalf("call %d criteria.To() = %v, want %v", callIndex, criteria.To(), snapshotTo)
			}
			wantOffset := callIndex * defaultTailBatchSize
			if criteria.Offset() != wantOffset {
				t.Fatalf("call %d criteria.Offset() = %d, want %d", callIndex, criteria.Offset(), wantOffset)
			}
		},
	}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	events, err := sut.listTailEventsSince(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).Build(),
		newTailCursor(cursorTime),
		snapshotTo,
	)
	if err != nil {
		t.Fatalf("listTailEventsSince() error = %v", err)
	}
	if len(events) != defaultTailBatchSize*2+1 {
		t.Fatalf("len(events) = %d, want %d", len(events), defaultTailBatchSize*2+1)
	}
	if len(eventStub.listCalls) != 3 {
		t.Fatalf("len(listCalls) = %d, want 3", len(eventStub.listCalls))
	}
}

func mustTailEvent(t *testing.T, eventID string, client string, agent string, sessionID string, workspace string, body string, createdAt time.Time) *model.Event {
	t.Helper()

	resolvedEventID, err := types.EventIDOf(eventID)
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	resolvedAgent, err := types.AgentOf(agent)
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	resolvedSessionID, err := types.SessionIDOf(sessionID)
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	return model.EventOf(
		resolvedEventID,
		types.EventKindNote,
		types.Client(client),
		resolvedAgent,
		resolvedSessionID,
		types.Workspace(workspace),
		body,
		createdAt,
	)
}
