package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

func TestLoadCockpitHome_AggregatesTopSignalsWithoutTTY(t *testing.T) {
	now := fixedStartedAt.Add(48 * time.Hour)
	previousTopNow := topNowFunc
	topNowFunc = func() time.Time { return now }
	t.Cleanup(func() { topNowFunc = previousTopNow })

	fresh := sessionSummaryFixture("fresh", "", now.Add(-time.Hour), "active", domtypes.EventKindTranscript, "fresh")
	stale := sessionSummaryFixture("stale", "", now.Add(-30*time.Hour), "stale", domtypes.EventKindTranscript, "stale")
	session := &topDataSessionStub{
		listResult: []apptypes.SessionSummary{fresh, stale},
		lineageByID: map[domtypes.SessionID][]apptypes.SessionSummary{
			domtypes.SessionID("fresh"): {fresh},
			domtypes.SessionID("stale"): {stale},
		},
	}

	hugeFailure := mustEvent(t, "evt-fail", domtypes.EventKindCommandExecuted, strings.Repeat("f", apptypes.DefaultTopSnapshotBodyLimit+1))
	command := mustEvent(t, "evt-cmd", domtypes.EventKindCommandExecuted, "go test ./...")
	event := &snapshotEventStub{failures: []*model.Event{hugeFailure}, commands: []*model.Event{command}}

	accepted := memorySummaryWithUpdatedAt(t, "mem-accepted", domtypes.MemoryStatusAccepted, now.Add(-2*time.Hour))
	candidate := memorySummaryWithUpdatedAt(t, "mem-candidate", domtypes.MemoryStatusCandidate, now.Add(-3*time.Hour))
	staleSummary := memorySummaryFixture(t, "mem-stale", domtypes.MemoryStatusSuperseded, "stale cockpit fixture")
	staleRow, err := apptypes.StaleMemoryRowOf(staleSummary, apptypes.StaleMemoryReasonSuperseded)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	staleResult, err := apptypes.StaleMemoryListResultOf(2, []apptypes.StaleMemoryRow{staleRow})
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}
	memory := &topDataMemoryStub{
		listFunc: func(criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
			statuses := criteria.Statuses()
			if len(statuses) == 1 && statuses[0] == domtypes.MemoryStatusCandidate {
				return []apptypes.MemorySummary{candidate}, nil
			}
			return []apptypes.MemorySummary{accepted, candidate}, nil
		},
		staleResult: staleResult,
	}

	root := &RootCLI{session: session, event: event, memory: memory}
	home, err := root.loadCockpitHome(context.Background(), cockpitCommandOptions{dbPath: filepath.Join(t.TempDir(), "traceary.db")})
	if err != nil {
		t.Fatalf("loadCockpitHome() error = %v", err)
	}

	if home.DoctorError == "" {
		t.Fatalf("DoctorError = empty, want explicit dependency/configuration signal when doctor cannot run")
	}
	if got, want := home.StaleActiveSessionCount, 1; got != want {
		t.Fatalf("StaleActiveSessionCount = %d, want %d", got, want)
	}
	if got, want := home.AcceptedMemoryCount, 1; got != want {
		t.Fatalf("AcceptedMemoryCount = %d, want %d", got, want)
	}
	if got, want := home.CandidateMemoryCount, 1; got != want {
		t.Fatalf("CandidateMemoryCount = %d, want %d", got, want)
	}
	if got, want := home.StaleMemoryCount, 2; got != want {
		t.Fatalf("StaleMemoryCount = %d, want %d", got, want)
	}
	if got, want := home.RecentFailureCount, 1; got != want {
		t.Fatalf("RecentFailureCount = %d, want %d", got, want)
	}
	if got, want := home.RecentCommandCount, 1; got != want {
		t.Fatalf("RecentCommandCount = %d, want %d", got, want)
	}
	if got, want := home.LargePayloadCount, 1; got != want {
		t.Fatalf("LargePayloadCount = %d, want %d", got, want)
	}
}

func TestCockpitHomeWarnings_ActionableWarningsFirst(t *testing.T) {
	t.Parallel()

	home := cockpitHomeSnapshot{
		DoctorFailCount:         1,
		HookFailCount:           1,
		DoctorWarnCount:         2,
		HookWarnCount:           1,
		StaleActiveSessionCount: 3,
		CandidateMemoryCount:    5,
		RecentFailureCount:      2,
		LargePayloadCount:       1,
	}
	warnings := home.warnings()
	if len(warnings) < 8 {
		t.Fatalf("warnings length = %d, want all actionable signals: %#v", len(warnings), warnings)
	}
	for i, warning := range warnings[:2] {
		if warning.severity != "FAIL" {
			t.Fatalf("warnings[%d].severity = %q, want FAIL first: %#v", i, warning.severity, warnings)
		}
	}
	for i, warning := range warnings[2:] {
		if warning.severity == "FAIL" {
			t.Fatalf("warnings[%d] is FAIL after WARN boundary: %#v", i+2, warnings)
		}
	}
	if !strings.Contains(warnings[0].hint, "doctor") {
		t.Fatalf("first warning should tell operator how to act, got %#v", warnings[0])
	}
}

func TestCockpitModelView_RendersStableEmptyStateAndOverview(t *testing.T) {
	t.Parallel()

	home := cockpitHomeSnapshot{
		LoadedAt:            fixedStartedAt,
		DBPath:              "/tmp/traceary.db",
		DoctorPassCount:     4,
		AcceptedMemoryCount: 2,
		RecentCommandCount:  1,
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	view := model.View()
	for _, must := range []string{
		"Traceary cockpit",
		"ATTENTION",
		"No immediate cockpit warnings",
		"OVERVIEW",
		"doctor: pass=4 warn=0 fail=0",
		"memories: accepted=2 candidate=0 stale=0",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("cockpit view missing %q:\n%s", must, view)
		}
	}
}

func TestCockpitModel_LivePaneRefreshFollowAndDetail(t *testing.T) {
	t.Parallel()

	initialEvent := mustEvent(t, "evt-initial", domtypes.EventKindNote, "initial live event")
	followEvent := mustEvent(t, "evt-follow", domtypes.EventKindCommandExecuted, "followed live event")
	loader := &cockpitLoaderStub{
		liveResponses: []cockpitLiveSnapshot{
			{Events: []*model.Event{initialEvent}, Cursor: newTailCursor(initialEvent.CreatedAt()), LoadedAt: fixedStartedAt},
			{Events: []*model.Event{initialEvent}, Cursor: newTailCursor(initialEvent.CreatedAt()), LoadedAt: fixedStartedAt.Add(time.Minute)},
			{Events: []*model.Event{followEvent}, Cursor: newTailCursor(followEvent.CreatedAt()), LoadedAt: fixedStartedAt.Add(2 * time.Minute)},
		},
		detailContent: topDetailContent{
			title: "EVENT evt-initial",
			lines: []string{"shared detail renderer line", "full event payload"},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("t"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || !model.live.loading {
		t.Fatalf("opening live pane mode/loading = %v/%v, want live/loading", model.mode, model.live.loading)
	}
	if cmd == nil {
		t.Fatalf("opening live pane returned nil command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("initial load command returned follow-up cmd = %T, want nil when follow is disabled", cmd)
	}
	if got, want := len(model.live.events), 1; got != want {
		t.Fatalf("live events after initial load = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "live tail") || !strings.Contains(got, "initial live event") {
		t.Fatalf("live view missing loaded event:\n%s", got)
	}
	if got := loader.liveCalls[0].initial; !got {
		t.Fatalf("first live call initial = false, want true")
	}

	updated, cmd = model.Update(cockpitRuneKey("r"))
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("refresh returned nil command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)
	if got := len(loader.liveCalls); got != 2 {
		t.Fatalf("live calls after refresh = %d, want 2", got)
	}
	if got := loader.liveCalls[1].initial; !got {
		t.Fatalf("refresh live call initial = false, want true")
	}

	updated, cmd = model.Update(cockpitRuneKey("f"))
	model = updated.(cockpitModel)
	if !model.live.follow || cmd == nil {
		t.Fatalf("follow toggle follow/cmd = %v/%T, want enabled/tick command", model.live.follow, cmd)
	}
	updated, cmd = model.Update(cockpitLiveTickMsg{})
	model = updated.(cockpitModel)
	if !model.live.loading || cmd == nil {
		t.Fatalf("follow tick loading/cmd = %v/%T, want loading/fetch command", model.live.loading, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if got := len(model.live.events); got != 2 {
		t.Fatalf("live events after follow poll = %d, want 2", got)
	}
	if got := model.View(); !strings.Contains(got, "followed live event") {
		t.Fatalf("live view missing followed event:\n%s", got)
	}
	if got := loader.liveCalls[2].initial; got {
		t.Fatalf("follow poll live call initial = true, want false")
	}
	if cmd == nil {
		t.Fatalf("follow poll should schedule the next tick while follow is enabled")
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeDetail || !model.detail.loading {
		t.Fatalf("opening detail mode/loading = %v/%v, want detail/loading", model.mode, model.detail.loading)
	}
	if cmd == nil {
		t.Fatalf("opening detail returned nil command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("detail load returned unexpected follow-up command = %T", cmd)
	}
	if got, want := loader.detailCalls, []domtypes.EventID{initialEvent.EventID()}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("detail calls = %v, want %v", got, want)
	}
	if got := model.View(); !strings.Contains(got, "shared detail renderer line") {
		t.Fatalf("detail view missing shared renderer output:\n%s", got)
	}

	updated, cmd = model.Update(cockpitRuneKey("t"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("returning to live with follow mode/cmd = %v/%T, want live/tick command", model.mode, cmd)
	}
}

func TestCockpitModel_IgnoresStaleLiveLoadResponses(t *testing.T) {
	t.Parallel()

	staleEvent := mustEvent(t, "evt-stale", domtypes.EventKindNote, "stale live event")
	freshEvent := mustEvent(t, "evt-fresh", domtypes.EventKindNote, "fresh live event")
	cockpit := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	cockpit.loader = &cockpitLoaderStub{}
	cockpit.loaderCtx = context.Background()

	updated, _ := cockpit.Update(cockpitRuneKey("t"))
	cockpit = updated.(cockpitModel)
	firstSeq := cockpit.live.requestSeq

	updated, _ = cockpit.Update(cockpitRuneKey("r"))
	cockpit = updated.(cockpitModel)
	secondSeq := cockpit.live.requestSeq
	if firstSeq == secondSeq {
		t.Fatalf("live request sequence did not advance: first=%d second=%d", firstSeq, secondSeq)
	}

	updated, _ = cockpit.Update(cockpitLiveMsg{
		seq:     firstSeq,
		initial: true,
		snapshot: cockpitLiveSnapshot{
			Events:   []*model.Event{staleEvent},
			Cursor:   newTailCursor(staleEvent.CreatedAt()),
			LoadedAt: fixedStartedAt,
		},
	})
	cockpit = updated.(cockpitModel)
	if len(cockpit.live.events) != 0 {
		t.Fatalf("stale response mutated live events: %#v", cockpit.live.events)
	}
	if !cockpit.live.loading {
		t.Fatalf("stale response cleared loading for the newer request")
	}

	updated, _ = cockpit.Update(cockpitLiveMsg{
		seq:     secondSeq,
		initial: true,
		snapshot: cockpitLiveSnapshot{
			Events:   []*model.Event{freshEvent},
			Cursor:   newTailCursor(freshEvent.CreatedAt()),
			LoadedAt: fixedStartedAt.Add(time.Minute),
		},
	})
	cockpit = updated.(cockpitModel)
	if got, want := len(cockpit.live.events), 1; got != want {
		t.Fatalf("current response live events = %d, want %d", got, want)
	}
	if got := cockpit.live.events[0].EventID(); got != freshEvent.EventID() {
		t.Fatalf("live event after current response = %s, want %s", got, freshEvent.EventID())
	}
	if cockpit.live.loading {
		t.Fatalf("current response left loading true")
	}
}

func TestFormatCockpitLiveEventRow_TruncatesLargePayload(t *testing.T) {
	t.Parallel()

	event := mustEvent(t, "evt-large", domtypes.EventKindCommandExecuted, strings.Repeat("x", apptypes.DefaultTopSnapshotBodyLimit+20))
	row := formatCockpitLiveEventRow(event, time.UTC)
	if !strings.Contains(row, "[truncated]") {
		t.Fatalf("row = %q, want truncation marker", row)
	}
	if got, limit := len([]rune(row)), apptypes.DefaultTopSnapshotBodyLimit; got >= limit {
		t.Fatalf("row length = %d, want compact row below body limit %d", got, limit)
	}
}

type cockpitLoaderStub struct {
	liveResponses []cockpitLiveSnapshot
	liveCalls     []cockpitLiveCall
	liveErr       error

	detailContent topDetailContent
	detailErr     error
	detailCalls   []domtypes.EventID
}

type cockpitLiveCall struct {
	cursor  tailCursor
	initial bool
}

func (s *cockpitLoaderStub) loadCockpitLive(_ context.Context, cursor tailCursor, initial bool) (cockpitLiveSnapshot, error) {
	s.liveCalls = append(s.liveCalls, cockpitLiveCall{cursor: cursor, initial: initial})
	if s.liveErr != nil {
		return cockpitLiveSnapshot{}, s.liveErr
	}
	if len(s.liveResponses) == 0 {
		return cockpitLiveSnapshot{LoadedAt: fixedStartedAt}, nil
	}
	next := s.liveResponses[0]
	s.liveResponses = s.liveResponses[1:]
	return next, nil
}

func (s *cockpitLoaderStub) loadCockpitEventDetail(_ context.Context, eventID domtypes.EventID) (topDetailContent, error) {
	s.detailCalls = append(s.detailCalls, eventID)
	return s.detailContent, s.detailErr
}

func cockpitRuneKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
