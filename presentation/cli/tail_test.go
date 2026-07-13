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
	listResponses       [][]*model.Event
	listErrs            []error
	listCalls           []apptypes.EventListCriteria
	onList              func(callIndex int, criteria apptypes.EventListCriteria)
	listWindowResponses [][]*model.Event
	listWindowErrs      []error
	listWindowCalls     []apptypes.EventListCriteria
	onListWindow        func(callIndex int, criteria apptypes.EventListCriteria)
}

func (s *tailEventUsecaseStub) Log(context.Context, string, types.EventKind, types.Client, types.Agent, types.SessionID, types.Workspace, apptypes.LogRedaction) (*model.Event, error) {
	return nil, nil
}

func (s *tailEventUsecaseStub) Audit(context.Context, apptypes.AuditInput, apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
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

func (s *tailEventUsecaseStub) ListWindow(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	callIndex := len(s.listWindowCalls)
	s.listWindowCalls = append(s.listWindowCalls, criteria)
	if s.onListWindow != nil {
		s.onListWindow(callIndex, criteria)
	}
	if callIndex < len(s.listWindowErrs) && s.listWindowErrs[callIndex] != nil {
		return nil, s.listWindowErrs[callIndex]
	}
	if callIndex < len(s.listWindowResponses) {
		return s.listWindowResponses[callIndex], nil
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
func (tailStoreManagementStub) CollectGarbage(context.Context, time.Time, apptypes.GarbageCollectionTarget, bool) (apptypes.CollectGarbageResult, error) {
	return apptypes.CollectGarbageResult{}, nil
}
func (tailStoreManagementStub) CloseStaleSessions(context.Context, time.Duration, bool, []types.SessionID) (apptypes.CloseStaleSessionsResult, error) {
	return apptypes.CloseStaleSessionsResult{}, nil
}
func (tailStoreManagementStub) DedupeContentEvents(context.Context, apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (tailStoreManagementStub) RestoreContentEventDedupeRun(context.Context, string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
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
		},
		onList: func(callIndex int, _ apptypes.EventListCriteria) {
			if callIndex == 0 {
				close(firstListDone)
			}
		},
		listWindowResponses: [][]*model.Event{
			{followup, initialNewer},
		},
		onListWindow: func(_ int, criteria apptypes.EventListCriteria) {
			cancel()
			if got := criteria.From(); !got.Equal(initialNewer.CreatedAt()) {
				t.Fatalf("criteria.From() = %v, want %v", got, initialNewer.CreatedAt())
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
		errCh <- sut.runTail(ctx, &bytes.Buffer{}, stdout, tailCommandInput{
			dbPath:        "/tmp/test-traceary.db",
			limit:         2,
			client:        "cli",
			agent:         "codex",
			sessionID:     "session-1",
			repo:          "duck8823/traceary",
			wide:          true,
			utc:           true,
			nowFunc:       func() time.Time { return startTime },
			tickerFactory: func(time.Duration) tailTicker { return ticker },
		})
	}()

	<-firstListDone
	ticker.ch <- time.Now()

	if err := <-errCh; err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tSOURCE_HOOK\tMESSAGE\n" +
		"2026-04-13T18:00:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\t-\tolder\n" +
		"2026-04-13T18:05:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\t-\tnewer\n" +
		"2026-04-13T18:10:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\t-\tlatest\n"
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
		listWindowResponses: [][]*model.Event{
			{newer, older},
		},
		onListWindow: func(callIndex int, _ apptypes.EventListCriteria) {
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
		errCh <- sut.runTail(ctx, &bytes.Buffer{}, stdout, tailCommandInput{
			dbPath:        "/tmp/test-traceary.db",
			limit:         0,
			repo:          "duck8823/traceary",
			wide:          true,
			utc:           true,
			nowFunc:       func() time.Time { return startTime },
			tickerFactory: func(time.Duration) tailTicker { return ticker },
		})
	}()

	ticker.ch <- time.Now()
	<-firstPoll

	if err := <-errCh; err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tSOURCE_HOOK\tMESSAGE\n" +
		"2026-04-13T19:45:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\t-\tnew\n"
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

	if err := sut.runTail(ctx, &bytes.Buffer{}, stdout, tailCommandInput{
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

func TestRunTail_DoesNotReEmitBoundaryEventWhenFromEqualsInitialTimestamp(t *testing.T) {
	t.Parallel()

	// Regression for #491: EventListCriteria.From() is inclusive, so the
	// follow-mode poll that runs after the initial fetch re-queries the same
	// timestamp as the last initial event. The tail cursor must rely on its
	// seenIDs set to drop that boundary event; otherwise the event gets
	// re-emitted every tick. This test forces that exact scenario end-to-end
	// through runTail, not just pollTailEvents in isolation, so a future
	// refactor that drops the seenIDs filter or flips From to exclusive on
	// one side of the stack is still caught.

	startTime := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	boundaryTime := time.Date(2026, 4, 13, 18, 5, 0, 0, time.UTC)
	ticker := newFakeTailTicker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialEvent := mustTailEvent(t, "event-boundary", "cli", "codex", "session-1", "duck8823/traceary", "boundary", boundaryTime)

	firstListDone := make(chan struct{})
	eventStub := &tailEventUsecaseStub{
		listResponses: [][]*model.Event{
			{initialEvent},
		},
		onList: func(callIndex int, _ apptypes.EventListCriteria) {
			if callIndex == 0 {
				close(firstListDone)
			}
		},
		// Simulate the query service honouring From() inclusively: it returns
		// the very same initialEvent plus a nothing-else payload, mirroring
		// what a real backend would yield when From==boundaryTime.
		listWindowResponses: [][]*model.Event{
			{initialEvent},
		},
		onListWindow: func(_ int, criteria apptypes.EventListCriteria) {
			cancel()
			if got := criteria.From(); !got.Equal(boundaryTime) {
				t.Fatalf("criteria.From() = %v, want %v", got, boundaryTime)
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
		errCh <- sut.runTail(ctx, &bytes.Buffer{}, stdout, tailCommandInput{
			dbPath:        "/tmp/test-traceary.db",
			limit:         1,
			client:        "cli",
			agent:         "codex",
			sessionID:     "session-1",
			repo:          "duck8823/traceary",
			wide:          true,
			utc:           true,
			nowFunc:       func() time.Time { return startTime },
			tickerFactory: func(time.Duration) tailTicker { return ticker },
		})
	}()

	<-firstListDone
	ticker.ch <- time.Now()

	if err := <-errCh; err != nil {
		t.Fatalf("runTail() error = %v", err)
	}

	want := "CREATED_AT\tKIND\tCLIENT\tAGENT\tSESSION_ID\tWORKSPACE\tSOURCE_HOOK\tMESSAGE\n" +
		"2026-04-13T18:05:00Z\tnote\tcli\tcodex\tsession-1\tduck8823/traceary\t-\tboundary\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q (event must appear exactly once despite From()==cursor.timestamp)", stdout.String(), want)
	}
}

func TestPollTailEvents_DeduplicatesSeenIDsAtSameTimestamp(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2026, 4, 13, 18, 5, 0, 0, time.UTC)
	seen := mustTailEvent(t, "event-seen", "cli", "codex", "session-1", "duck8823/traceary", "seen", timestamp)
	fresh := mustTailEvent(t, "event-fresh", "cli", "codex", "session-1", "duck8823/traceary", "fresh", timestamp)

	eventStub := &tailEventUsecaseStub{
		listWindowResponses: [][]*model.Event{
			{fresh, seen},
		},
	}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	cursor := newTailCursor(timestamp)
	cursor.seenIDs[seen.EventID().String()] = struct{}{}

	events, err := sut.pollTailEvents(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).Build(),
		cursor,
		timestamp.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("pollTailEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if got := events[0].EventID().String(); got != fresh.EventID().String() {
		t.Fatalf("EventID() = %q, want %q", got, fresh.EventID().String())
	}
}

func TestPollTailEvents_DelegatesPagingToWindowQuery(t *testing.T) {
	t.Parallel()

	cursorTime := time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC)
	snapshotTo := time.Date(2026, 4, 13, 18, 10, 0, 0, time.UTC)

	// ListWindow now returns the whole window in a single call. Feeding it a
	// multi-batch-sized payload verifies tail.go no longer performs its own
	// offset pagination and simply trusts the query service's stable snapshot.
	events := make([]*model.Event, 0, defaultTailBatchSize*2+1)
	for i := range defaultTailBatchSize*2 + 1 {
		events = append(events, mustTailEvent(
			t,
			"event-"+strconv.Itoa(i),
			"cli",
			"codex",
			"session-1",
			"duck8823/traceary",
			"body",
			cursorTime.Add(time.Duration(i+1)*time.Second),
		))
	}

	eventStub := &tailEventUsecaseStub{
		listWindowResponses: [][]*model.Event{events},
		onListWindow: func(_ int, criteria apptypes.EventListCriteria) {
			if !criteria.To().Equal(snapshotTo) {
				t.Fatalf("criteria.To() = %v, want %v", criteria.To(), snapshotTo)
			}
			if !criteria.From().Equal(cursorTime) {
				t.Fatalf("criteria.From() = %v, want %v", criteria.From(), cursorTime)
			}
			if criteria.Offset() != 0 {
				t.Fatalf("criteria.Offset() = %d, want 0", criteria.Offset())
			}
		},
	}
	sut := NewRootCLI(
		WithStoreManagement(tailStoreManagementStub{}),
		WithEvent(eventStub),
	)

	got, err := sut.pollTailEvents(
		context.Background(),
		apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).Build(),
		newTailCursor(cursorTime),
		snapshotTo,
	)
	if err != nil {
		t.Fatalf("pollTailEvents() error = %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(events))
	}
	if len(eventStub.listWindowCalls) != 1 {
		t.Fatalf("len(listWindowCalls) = %d, want 1", len(eventStub.listWindowCalls))
	}
}

func mustTailEvent(t *testing.T, eventID string, client string, agent string, sessionID string, workspace string, body string, createdAt time.Time) *model.Event {
	t.Helper()

	resolvedEventID, err := types.EventIDFrom(eventID)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	resolvedAgent, err := types.AgentFrom(agent)
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	resolvedSessionID, err := types.SessionIDFrom(sessionID)
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
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
