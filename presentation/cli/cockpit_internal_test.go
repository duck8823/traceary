package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

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
	if home.NewCandidateMemoryKnown {
		t.Fatalf("NewCandidateMemoryKnown = true, want false when cockpit state is not configured")
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

func TestLoadCockpitHome_MemoryNotificationsUseLastSeenWhenAvailable(t *testing.T) {
	now := fixedStartedAt.Add(72 * time.Hour)
	previousTopNow := topNowFunc
	topNowFunc = func() time.Time { return now }
	t.Cleanup(func() { topNowFunc = previousTopNow })

	lastSeenAt := now.Add(-2 * time.Hour)
	accepted := memorySummaryWithSourceAndUpdatedAt(t, "mem-accepted", domtypes.MemoryStatusAccepted, domtypes.MemorySourceManual, now.Add(-30*time.Minute))
	oldCandidate := memorySummaryWithSourceAndUpdatedAt(t, "mem-old", domtypes.MemoryStatusCandidate, domtypes.MemorySourceExtracted, now.Add(-3*time.Hour))
	newRemember := memorySummaryWithSourceAndUpdatedAt(t, "mem-remember", domtypes.MemoryStatusCandidate, domtypes.MemorySourceRememberIntent, now.Add(-90*time.Minute))
	newLowQuality := memorySummaryWithSourceAndUpdatedAt(t, "mem-low", domtypes.MemoryStatusCandidate, domtypes.MemorySourceExtractedHidden, now.Add(-15*time.Minute))
	candidates := []apptypes.MemorySummary{oldCandidate, newRemember, newLowQuality}
	memory := &topDataMemoryStub{
		listFunc: func(criteria apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
			statuses := criteria.Statuses()
			if len(statuses) == 1 && statuses[0] == domtypes.MemoryStatusCandidate {
				return candidates, nil
			}
			return []apptypes.MemorySummary{accepted, oldCandidate, newRemember, newLowQuality}, nil
		},
	}
	root := &RootCLI{
		memory:       memory,
		cockpitState: cockpitStateReaderStub{at: lastSeenAt, ok: true},
	}

	home, err := root.loadCockpitHome(context.Background(), cockpitCommandOptions{dbPath: filepath.Join(t.TempDir(), "traceary.db")})
	if err != nil {
		t.Fatalf("loadCockpitHome() error = %v", err)
	}

	if !home.NewCandidateMemoryKnown {
		t.Fatalf("NewCandidateMemoryKnown = false, want true")
	}
	if got, want := home.MemoryLastSeenAt, lastSeenAt; !got.Equal(want) {
		t.Fatalf("MemoryLastSeenAt = %v, want %v", got, want)
	}
	if got, want := home.AcceptedMemoryCount, 1; got != want {
		t.Fatalf("AcceptedMemoryCount = %d, want %d", got, want)
	}
	if got, want := home.CandidateMemoryCount, 3; got != want {
		t.Fatalf("CandidateMemoryCount = %d, want %d", got, want)
	}
	if got, want := home.NewCandidateMemoryCount, 2; got != want {
		t.Fatalf("NewCandidateMemoryCount = %d, want %d", got, want)
	}
	if got, want := home.RememberIntentCount, 1; got != want {
		t.Fatalf("RememberIntentCount = %d, want %d", got, want)
	}
	if got, want := home.LowQualityMemoryCount, 1; got != want {
		t.Fatalf("LowQualityMemoryCount = %d, want %d", got, want)
	}

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.mode = cockpitModeTop
	view := model.View()
	for _, must := range []string{
		"new memory candidates=2",
		"remember-intent candidates=1",
		"low-quality candidates=1",
		"memories: accepted(reviewed)=1 candidate(inbox)=3 new=2 remember-intent=1 low-quality=1 stale=0",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("cockpit view missing %q:\n%s", must, view)
		}
	}
}

func TestLoadCockpitHome_EventNotificationsUseLastSeenWhenAvailable(t *testing.T) {
	t.Parallel()

	lastSeenAt := fixedStartedAt.Add(-2 * time.Hour)
	oldEvent := mustEvent(t, "evt-old", domtypes.EventKindNote, "old event")
	oldEvent = model.EventOfWithSourceHook(oldEvent.EventID(), oldEvent.Kind(), oldEvent.Client(), oldEvent.Agent(), oldEvent.SessionID(), oldEvent.Workspace(), oldEvent.Body(), lastSeenAt, oldEvent.SourceHook())
	newEvent := mustEvent(t, "evt-new", domtypes.EventKindNote, "new event")
	event := &snapshotEventStub{events: []*model.Event{newEvent, oldEvent}}
	root := &RootCLI{
		event:        event,
		cockpitState: cockpitStateReaderStub{eventAt: lastSeenAt, eventOk: true, eventSeenIDs: []string{oldEvent.EventID().String()}},
	}

	home, err := root.loadCockpitHome(context.Background(), cockpitCommandOptions{dbPath: filepath.Join(t.TempDir(), "traceary.db")})
	if err != nil {
		t.Fatalf("loadCockpitHome() error = %v", err)
	}
	if !home.NewEventKnown {
		t.Fatalf("NewEventKnown = false, want true")
	}
	if got, want := home.NewEventCount, 1; got != want {
		t.Fatalf("NewEventCount = %d, want %d", got, want)
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.mode = cockpitModeTop
	view := model.View()
	if !strings.Contains(view, "new events=1") || !strings.Contains(view, "new_events=1") {
		t.Fatalf("cockpit view missing new event notification:\n%s", view)
	}
}

func TestLoadCockpitHome_EventNotificationsScanPastSeenBoundaryIDs(t *testing.T) {
	t.Parallel()

	lastSeenAt := fixedStartedAt.Add(-2 * time.Hour)
	seenIDs := make([]string, 0, cockpitNewEventLimit+5)
	events := make([]*model.Event, 0, (cockpitNewEventLimit*2)+6)
	for i := range cockpitNewEventLimit + 5 {
		event := cockpitEventFixtureAt(t, fmt.Sprintf("evt-seen-%03d", i), lastSeenAt)
		seenIDs = append(seenIDs, event.EventID().String())
		events = append(events, event)
	}
	for i := range cockpitNewEventLimit + 1 {
		events = append(events, cockpitEventFixtureAt(t, fmt.Sprintf("evt-new-%03d", i), lastSeenAt))
	}
	root := &RootCLI{
		event:        &snapshotEventStub{events: events},
		cockpitState: cockpitStateReaderStub{eventAt: lastSeenAt, eventOk: true, eventSeenIDs: seenIDs},
	}

	home, err := root.loadCockpitHome(context.Background(), cockpitCommandOptions{dbPath: filepath.Join(t.TempDir(), "traceary.db")})
	if err != nil {
		t.Fatalf("loadCockpitHome() error = %v", err)
	}
	if got, want := home.NewEventCount, cockpitNewEventLimit; got != want {
		t.Fatalf("NewEventCount = %d, want capped %d", got, want)
	}
	if !home.NewEventScanLimited {
		t.Fatalf("NewEventScanLimited = false, want true")
	}
}

func TestLoadCockpitHome_MemoryNotificationsFallbackWhenLastSeenUnavailable(t *testing.T) {
	t.Parallel()

	candidate := memorySummaryWithSourceAndUpdatedAt(t, "mem-candidate", domtypes.MemoryStatusCandidate, domtypes.MemorySourceExtracted, fixedStartedAt)
	memory := &topDataMemoryStub{
		listFunc: func(_ apptypes.MemoryListCriteria) ([]apptypes.MemorySummary, error) {
			return []apptypes.MemorySummary{candidate}, nil
		},
	}
	root := &RootCLI{memory: memory}

	home, err := root.loadCockpitHome(context.Background(), cockpitCommandOptions{dbPath: filepath.Join(t.TempDir(), "traceary.db")})
	if err != nil {
		t.Fatalf("loadCockpitHome() error = %v", err)
	}
	if home.NewCandidateMemoryKnown {
		t.Fatalf("NewCandidateMemoryKnown = true, want false")
	}
	if got, want := formatCockpitNewCandidateCount(home), "untracked"; got != want {
		t.Fatalf("formatCockpitNewCandidateCount() = %q, want %q", got, want)
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.mode = cockpitModeTop
	view := model.View()
	if !strings.Contains(view, "memory candidates=1") {
		t.Fatalf("fallback view should still surface total candidates:\n%s", view)
	}
	if strings.Contains(view, "new memory candidates=") {
		t.Fatalf("fallback view should not claim a new count without last-seen state:\n%s", view)
	}
	if !strings.Contains(view, "memory candidate new count=untracked") {
		t.Fatalf("fallback view should explain untracked memory notification state:\n%s", view)
	}
	if !strings.Contains(view, "new=untracked") {
		t.Fatalf("fallback view missing untracked new-count state:\n%s", view)
	}
}

func TestCockpitModelView_MemoryNotificationsNoneState(t *testing.T) {
	t.Parallel()

	home := cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		AcceptedMemoryCount:     2,
		NewCandidateMemoryKnown: true,
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.mode = cockpitModeTop
	view := model.View()
	if !strings.Contains(view, "memories: accepted(reviewed)=2 candidate(inbox)=0 new=0 remember-intent=0 low-quality=0 stale=0") {
		t.Fatalf("none-state view missing zero notification summary:\n%s", view)
	}
	if strings.Contains(view, "[WARN] Memory review") || strings.Contains(view, "new memory candidates=") {
		t.Fatalf("none-state view should not show candidate warnings:\n%s", view)
	}
	if !strings.Contains(view, "no unseen candidates since not recorded") {
		t.Fatalf("none-state view should explain seen memory checkpoint:\n%s", view)
	}
}

func TestCockpitModelView_RendersActionableTriageBoard(t *testing.T) {
	t.Parallel()

	home := cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		DBPath:                  "/tmp/traceary.db",
		DoctorPassCount:         3,
		DoctorWarnCount:         1,
		DoctorFailCount:         1,
		HookFailCount:           1,
		StaleActiveSessionCount: 2,
		CandidateMemoryCount:    4,
		NewCandidateMemoryCount: 2,
		NewCandidateMemoryKnown: true,
		MemoryLastSeenAt:        fixedStartedAt.Add(-time.Hour),
		RememberIntentCount:     1,
		LowQualityMemoryCount:   1,
		NewEventCount:           3,
		NewEventKnown:           true,
		EventLastSeenAt:         fixedStartedAt.Add(-30 * time.Minute),
		RecentFailureCount:      2,
		RecentCommandCount:      5,
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.mode = cockpitModeTop
	view := model.View()
	for _, must := range []string{
		"Traceary cockpit · top",
		"Top summary",
		"sessions: stale_active=2 recent_failures=2 recent_commands=5 new_events=3",
		"memories: accepted(reviewed)=0 candidate(inbox)=4 new=2 remember-intent=1 low-quality=1 stale=0",
		"doctor: pass=3 warn=1 fail=1",
		"hooks/mcp: warn=0 fail=1",
		"Actionable signals",
		"[FAIL] Health failures",
		"(d open Doctor checks)",
		"[WARN] Memory review queue needs attention",
		"(3 open Memory review)",
		"[WARN] Recent command failures",
		"[WARN] Stale active sessions",
		"[INFO] New Tail events",
		"new events=3 since 2026-05-07T11:30:00Z",
		"new memory candidates=2",
		"remember-intent candidates=1",
		"low-quality candidates=1",
		"stale active sessions=2",
		"recent failures=2",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("top summary missing %q:\n%s", must, view)
		}
	}
	for _, mustNot := range []string{"TRIAGE BOARD", "1 Home"} {
		if strings.Contains(view, mustNot) {
			t.Fatalf("top summary should not render removed Home triage cue %q:\n%s", mustNot, view)
		}
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
	model.mode = cockpitModeTop
	view := model.View()
	for _, must := range []string{
		"Traceary cockpit",
		"tabs: 1 Tail  [2 Top]  3 Memory  4 Sessions  5 Settings",
		"Top summary",
		"Actionable signals",
		"[OK] No active signals",
		"Signal details",
		"doctor: pass=4 warn=0 fail=0",
		"memories: accepted(reviewed)=2 candidate(inbox)=0 new=untracked remember-intent=0 low-quality=0 stale=0",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("cockpit view missing %q:\n%s", must, view)
		}
	}
}

func TestCockpitModelView_SurfacesDoctorUnavailableSignal(t *testing.T) {
	t.Parallel()

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{
		LoadedAt:    fixedStartedAt,
		DBPath:      "/tmp/traceary.db",
		DoctorError: "doctor dependency unavailable",
	})
	model.mode = cockpitModeTop
	view := model.View()
	for _, must := range []string{
		"doctor: pass=0 warn=0 fail=0",
		"[FAIL] Doctor unavailable",
		"doctor dependency unavailable",
		"(d open Doctor checks)",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("doctor unavailable top view missing %q:\n%s", must, view)
		}
	}
}

func TestCockpitModelTopTab_LoadsDashboardAndOpensDetail(t *testing.T) {
	t.Parallel()

	session := sessionSummaryFixture("sess-top", "", fixedStartedAt, "active", domtypes.EventKindTranscript, "top session")
	failure := mustEvent(t, "evt-top-fail", domtypes.EventKindCommandExecuted, "go test failed in top tab")
	command := mustEvent(t, "evt-top-command", domtypes.EventKindCommandExecuted, "go test ./...")
	candidate := memorySummaryFixture(t, "mem-top-candidate", domtypes.MemoryStatusCandidate, "use the dedicated top dashboard")
	snapshot := topDataSnapshot{
		Sessions:       buildActiveSessionTreeWithOptions([]apptypes.SessionSummary{session}, false, defaultActiveSessionStaleAfter, fixedStartedAt),
		Failures:       []*model.Event{failure},
		RecentCommands: []*model.Event{command},
		Candidates:     []apptypes.MemorySummary{candidate},
		Now:            fixedStartedAt,
	}
	loader := &cockpitLoaderStub{
		topResponses: []topDataSnapshot{snapshot},
		topDetail:    topDetailContent{title: "EVENT evt-top-fail", lines: []string{"failure detail"}},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("2"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeTop || cmd == nil {
		t.Fatalf("open top mode/cmd = %v/%T, want top/load command", model.mode, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("top load returned follow-up command = %T", cmd)
	}
	view := model.View()
	for _, must := range []string{
		"Top dashboard",
		"SESSIONS (1)",
		"RECENT FAILURES (1)",
		"go test failed in top tab",
		"MEMORY CANDIDATES (1)",
		"use the dedicated top dashboard",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("top dashboard view missing %q:\n%s", must, view)
		}
	}
	if strings.Contains(view, "run `traceary top`") {
		t.Fatalf("top dashboard should not point users back to the top subcommand:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(cockpitModel)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("enter on top failure row returned nil command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("top detail load returned follow-up command = %T", cmd)
	}
	if got := len(loader.topDetails); got != 1 {
		t.Fatalf("top detail calls = %d, want 1", got)
	}
	if got := loader.topDetails[0].target.kind; got != topDetailEvent {
		t.Fatalf("top detail kind = %v, want event", got)
	}
	view = model.View()
	if !strings.Contains(view, "Traceary cockpit · top detail") || !strings.Contains(view, "failure detail") {
		t.Fatalf("top detail view missing loaded detail:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeTop || model.cockpitTopDetailOpen() {
		t.Fatalf("esc from top detail mode/detailOpen = %v/%v, want top/false", model.mode, model.cockpitTopDetailOpen())
	}
}

func TestCockpitModelTopTab_RefreshDoesNotResetTailSelection(t *testing.T) {
	t.Parallel()

	eventA := mustEvent(t, "evt-top-refresh-a", domtypes.EventKindNote, "first")
	eventB := mustEvent(t, "evt-top-refresh-b", domtypes.EventKindNote, "second")
	loader := &cockpitLoaderStub{topResponses: []topDataSnapshot{{Now: fixedStartedAt}}}
	cockpit := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	cockpit.loader = loader
	cockpit.loaderCtx = context.Background()
	cockpit.mode = cockpitModeTop
	cockpit.top.loaded = true
	cockpit.top.loadedAt = fixedStartedAt
	cockpit.live.events = []*model.Event{eventA, eventB}
	cockpit.live.selected = 1
	cockpit.live.follow = false
	liveSeq := cockpit.live.requestSeq

	updated, cmd := cockpit.Update(cockpitRuneKey("r"))
	cockpit = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("top refresh returned nil command")
	}
	if cockpit.live.selected != 1 || cockpit.live.follow {
		t.Fatalf("tail state after top refresh selected/follow = %d/%v, want 1/false", cockpit.live.selected, cockpit.live.follow)
	}
	if cockpit.live.requestSeq != liveSeq {
		t.Fatalf("live request sequence changed on top refresh: got %d want %d", cockpit.live.requestSeq, liveSeq)
	}
	if !cockpit.top.loading {
		t.Fatalf("top.loading = false, want true after refresh")
	}
}

func TestCockpitModelTopTab_IgnoresStaleDetailAfterLeavingTop(t *testing.T) {
	t.Parallel()

	failure := mustEvent(t, "evt-top-stale-detail", domtypes.EventKindCommandExecuted, "stale detail response")
	loader := &cockpitLoaderStub{
		topResponses: []topDataSnapshot{{
			Failures: []*model.Event{failure},
			Now:      fixedStartedAt,
		}},
		topDetail: topDetailContent{title: "EVENT evt-top-stale-detail", lines: []string{"late detail"}},
	}
	cockpit := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	cockpit.loader = loader
	cockpit.loaderCtx = context.Background()

	updated, cmd := cockpit.Update(cockpitRuneKey("2"))
	cockpit = updated.(cockpitModel)
	updated, _ = cockpit.Update(cmd())
	cockpit = updated.(cockpitModel)
	updated, cmd = cockpit.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cockpit = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("enter on top failure row returned nil command")
	}
	updated, liveCmd := cockpit.Update(cockpitRuneKey("1"))
	cockpit = updated.(cockpitModel)
	if cockpit.mode != cockpitModeLive || cockpit.cockpitTopDetailOpen() || liveCmd == nil {
		t.Fatalf("leaving top mode/detail/liveCmd = %v/%v/%T, want live/false/cmd", cockpit.mode, cockpit.cockpitTopDetailOpen(), liveCmd)
	}
	updated, staleCmd := cockpit.Update(cmd())
	cockpit = updated.(cockpitModel)
	if staleCmd != nil {
		t.Fatalf("stale top detail returned follow-up command = %T", staleCmd)
	}
	if cockpit.mode != cockpitModeLive || cockpit.cockpitTopDetailOpen() {
		t.Fatalf("stale detail changed mode/detailOpen = %v/%v, want live/false", cockpit.mode, cockpit.cockpitTopDetailOpen())
	}
}

func TestCockpitModel_InitLoadsTailEvenWhenTopSummaryFails(t *testing.T) {
	t.Parallel()

	event := mustEvent(t, "evt-init-tail", domtypes.EventKindNote, "tail still opens")
	loader := &cockpitLoaderStub{
		homeErr:       context.Canceled,
		liveResponses: []cockpitLiveSnapshot{{Events: []*model.Event{event}, Cursor: newTailCursor(event.CreatedAt()), LoadedAt: fixedStartedAt.Add(time.Minute)}},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	cmd := model.Init()
	if cmd == nil {
		t.Fatalf("init command = nil, want top summary + tail load")
	}
	model = applyCockpitImmediateCommandForTest(t, model, cmd)
	if model.mode != cockpitModeLive {
		t.Fatalf("mode after init = %v, want tail", model.mode)
	}
	if model.live.loading {
		t.Fatalf("tail loading after init = true, want false")
	}
	if got, want := len(model.live.events), 1; got != want {
		t.Fatalf("tail events after init = %d, want %d", got, want)
	}
	if model.statusErr == "" {
		t.Fatalf("statusErr after failed top summary = empty, want non-empty")
	}
	if got, want := loader.homeCalls, 1; got != want {
		t.Fatalf("home calls after init = %d, want %d", got, want)
	}
	if got, want := len(loader.liveCalls), 1; got != want {
		t.Fatalf("tail calls after init = %d, want %d", got, want)
	}
}

func TestCockpitModel_DoctorPaneRendersPassWarnFailSkipAndFixHints(t *testing.T) {
	t.Parallel()

	loader := &cockpitLoaderStub{
		doctorResponses: []cockpitDoctorSnapshot{
			cockpitDoctorSnapshot{
				LoadedAt: fixedStartedAt,
				DBPath:   "/tmp/traceary.db",
				Summary:  doctorSummary{Pass: 2, Warn: 1, Fail: 1},
				Sections: []cockpitDoctorSection{
					{
						Name: "Database",
						Checks: []cockpitDoctorCheck{
							{Name: "db-write", Status: doctorStatusPass, Severity: doctorSeverityPass, Message: "store is writable"},
							{Name: "stale-active-sessions", Status: doctorStatusWarn, Severity: doctorSeverityWarn, Message: "3 stale active sessions", Hint: "preview cleanup first", FixCommand: "traceary session gc --stale-after 24h --dry-run"},
						},
					},
					{
						Name: "Hooks",
						Checks: []cockpitDoctorCheck{
							{Name: "claude-config", Status: doctorStatusFail, Severity: doctorSeverityFail, Message: "claude config is invalid", Hint: "repair settings.json"},
							{Name: "claude-global-config", Status: doctorStatusSkip, Severity: doctorSeverityPass, Message: "global config not present"},
						},
					},
				},
			},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("d"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeDoctor || !model.doctor.loading || cmd == nil {
		t.Fatalf("doctor launch mode/loading/cmd = %v/%v/%T, want doctor/loading/cmd", model.mode, model.doctor.loading, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("doctor load returned follow-up command = %T", cmd)
	}
	if got, want := loader.doctorCalls, 1; got != want {
		t.Fatalf("doctor calls = %d, want %d", got, want)
	}
	view := model.View()
	for _, must := range []string{
		"Traceary cockpit · doctor",
		"summary: pass=2 warn=1 fail=1",
		"[PASS] db-write",
		"[WARN] stale-active-sessions",
		"[FAIL] claude-config",
		"[SKIP] claude-global-config",
		"hint: preview cleanup first",
		"fix: traceary session gc --stale-after 24h --dry-run",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("doctor view missing %q:\n%s", must, view)
		}
	}

	updated, cmd = model.Update(cockpitRuneKey("2"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeTop || cmd == nil {
		t.Fatalf("2 from doctor mode/cmd = %v/%T, want top/top load command", model.mode, cmd)
	}
}

func TestCockpitModel_DoctorPaneRefreshesOnDemand(t *testing.T) {
	t.Parallel()

	loader := &cockpitLoaderStub{
		doctorResponses: []cockpitDoctorSnapshot{
			{
				LoadedAt: fixedStartedAt,
				Summary:  doctorSummary{Pass: 1},
				Sections: []cockpitDoctorSection{{Name: "Environment", Checks: []cockpitDoctorCheck{
					{Name: "path", Status: doctorStatusPass, Severity: doctorSeverityPass, Message: "traceary is on PATH"},
				}}},
			},
			{
				LoadedAt: fixedStartedAt.Add(time.Minute),
				Summary:  doctorSummary{Pass: 0, Warn: 1},
				Sections: []cockpitDoctorSection{{Name: "Hooks", Checks: []cockpitDoctorCheck{
					{Name: "codex-config", Status: doctorStatusWarn, Severity: doctorSeverityWarn, Message: "codex hooks missing", FixCommand: "traceary hooks install --client codex"},
				}}},
			},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("d"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)
	if got := model.View(); !strings.Contains(got, "[PASS] path") {
		t.Fatalf("initial doctor view missing pass check:\n%s", got)
	}

	updated, cmd = model.Update(cockpitRuneKey("r"))
	model = updated.(cockpitModel)
	if !model.doctor.loading || cmd == nil {
		t.Fatalf("doctor refresh loading/cmd = %v/%T, want loading/cmd", model.doctor.loading, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("doctor refresh returned follow-up command = %T", cmd)
	}
	if got, want := loader.doctorCalls, 2; got != want {
		t.Fatalf("doctor calls after refresh = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "[WARN] codex-config") || !strings.Contains(got, "fix: traceary hooks install --client codex") {
		t.Fatalf("refreshed doctor view missing warning/fix:\n%s", got)
	}

	updated, cmd = model.Update(cockpitRuneKey("h"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd != nil {
		t.Fatalf("doctor h mode/cmd = %v/%T, want tail/nil", model.mode, cmd)
	}
}

func TestCockpitModel_IgnoresStaleDoctorResponses(t *testing.T) {
	t.Parallel()

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = &cockpitLoaderStub{}
	model.loaderCtx = context.Background()

	updated, _ := model.Update(cockpitRuneKey("d"))
	model = updated.(cockpitModel)
	firstSeq := model.doctor.requestSeq
	updated, _ = model.Update(cockpitRuneKey("r"))
	model = updated.(cockpitModel)
	secondSeq := model.doctor.requestSeq
	if firstSeq == secondSeq {
		t.Fatalf("doctor request sequence did not advance: first=%d second=%d", firstSeq, secondSeq)
	}

	updated, _ = model.Update(cockpitDoctorLoadedMsg{
		seq: firstSeq,
		snapshot: cockpitDoctorSnapshot{
			LoadedAt: fixedStartedAt,
			Summary:  doctorSummary{Fail: 1},
			Sections: []cockpitDoctorSection{{Name: "Hooks", Checks: []cockpitDoctorCheck{
				{Name: "old-config", Status: doctorStatusFail, Severity: doctorSeverityFail, Message: "old doctor response"},
			}}},
		},
	})
	model = updated.(cockpitModel)
	if !model.doctor.loading {
		t.Fatalf("stale doctor response cleared loading for the newer request")
	}
	if strings.Contains(model.View(), "old doctor response") {
		t.Fatalf("stale doctor response mutated view:\n%s", model.View())
	}

	updated, _ = model.Update(cockpitDoctorLoadedMsg{
		seq: secondSeq,
		snapshot: cockpitDoctorSnapshot{
			LoadedAt: fixedStartedAt.Add(time.Minute),
			Summary:  doctorSummary{Warn: 1},
			Sections: []cockpitDoctorSection{{Name: "Hooks", Checks: []cockpitDoctorCheck{
				{Name: "new-config", Status: doctorStatusWarn, Severity: doctorSeverityWarn, Message: "new doctor response"},
			}}},
		},
	})
	model = updated.(cockpitModel)
	if model.doctor.loading {
		t.Fatalf("current doctor response left loading true")
	}
	if got := model.View(); !strings.Contains(got, "new doctor response") || strings.Contains(got, "old doctor response") {
		t.Fatalf("doctor view did not use current response only:\n%s", got)
	}
}

func TestCockpitModel_LivePaneRefreshPauseResumeAndDetail(t *testing.T) {
	t.Parallel()

	olderEvent := mustEvent(t, "evt-older", domtypes.EventKindNote, "older live event")
	initialEvent := mustEvent(t, "evt-initial", domtypes.EventKindNote, "initial live event")
	followEvent := mustEvent(t, "evt-follow", domtypes.EventKindCommandExecuted, "followed live event")
	loader := &cockpitLoaderStub{
		liveResponses: []cockpitLiveSnapshot{
			{Events: []*model.Event{olderEvent, initialEvent}, Cursor: newTailCursor(initialEvent.CreatedAt()), LoadedAt: fixedStartedAt},
			{Events: []*model.Event{olderEvent, initialEvent}, Cursor: newTailCursor(initialEvent.CreatedAt()), LoadedAt: fixedStartedAt.Add(time.Minute)},
			{Events: []*model.Event{followEvent}, Cursor: newTailCursor(followEvent.CreatedAt()), LoadedAt: fixedStartedAt.Add(2 * time.Minute)},
		},
		detailContent: topDetailContent{
			title: "EVENT evt-follow",
			lines: []string{"shared detail renderer line", "full event payload"},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()
	model.mode = cockpitModeTop

	updated, cmd := model.Update(cockpitRuneKey("t"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || !model.live.loading || !model.live.follow {
		t.Fatalf("opening live pane mode/loading/follow = %v/%v/%v, want live/loading/follow", model.mode, model.live.loading, model.live.follow)
	}
	if cmd == nil {
		t.Fatalf("opening live pane returned nil command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("initial load should mark events and schedule next poll")
	}
	runFirstCockpitBatchCommandForTest(t, cmd)
	if got, want := len(loader.eventSeenCalls), 1; got != want {
		t.Fatalf("event seen calls after initial follow load = %d, want %d", got, want)
	}
	if got, want := len(model.live.events), 2; got != want {
		t.Fatalf("live events after initial load = %d, want %d", got, want)
	}
	if got, want := model.live.selected, 1; got != want {
		t.Fatalf("selected row after default follow load = %d, want newest %d", got, want)
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
	if !model.live.follow {
		t.Fatalf("refresh disabled default auto-follow")
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(cockpitModel)
	if model.live.follow || cmd != nil {
		t.Fatalf("scroll up follow/cmd = %v/%T, want paused/nil", model.live.follow, cmd)
	}
	if got, want := model.live.selected, 0; got != want {
		t.Fatalf("selected row after scroll up = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "Auto-follow paused") {
		t.Fatalf("paused live view missing pause cue:\n%s", got)
	}

	updated, cmd = model.Update(cockpitLiveTickMsg{})
	model = updated.(cockpitModel)
	if !model.live.loading || cmd == nil {
		t.Fatalf("paused poll tick loading/cmd = %v/%T, want loading/fetch command", model.live.loading, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if got := len(model.live.events); got != 3 {
		t.Fatalf("live events after paused poll = %d, want 3", got)
	}
	if got, want := model.live.selected, 0; got != want {
		t.Fatalf("selected row after paused poll = %d, want paused row %d", got, want)
	}
	if got, want := model.live.pausedNewCount, 1; got != want {
		t.Fatalf("paused new count = %d, want %d", got, want)
	}
	if got, want := len(loader.eventSeenCalls), 1; got != want {
		t.Fatalf("event seen calls after paused poll = %d, want still %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "1 newer event") {
		t.Fatalf("paused live view missing newer-event cue:\n%s", got)
	}
	if got := loader.liveCalls[2].initial; got {
		t.Fatalf("paused poll live call initial = true, want false")
	}
	if cmd == nil {
		t.Fatalf("paused poll should schedule the next tick")
	}

	updated, cmd = model.Update(cockpitRuneKey("G"))
	model = updated.(cockpitModel)
	if !model.live.follow || model.live.pausedNewCount != 0 || cmd == nil {
		t.Fatalf("G resume follow/new/cmd = %v/%d/%T, want follow/0/tick", model.live.follow, model.live.pausedNewCount, cmd)
	}
	runFirstCockpitBatchCommandForTest(t, cmd)
	if got, want := len(loader.eventSeenCalls), 2; got != want {
		t.Fatalf("event seen calls after G resume = %d, want %d", got, want)
	}
	if got, want := model.live.selected, 2; got != want {
		t.Fatalf("selected row after G = %d, want newest %d", got, want)
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
	if got, want := loader.detailCalls, []domtypes.EventID{followEvent.EventID()}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("detail calls = %v, want %v", got, want)
	}
	if got := model.View(); !strings.Contains(got, "shared detail renderer line") {
		t.Fatalf("detail view missing shared renderer output:\n%s", got)
	}

	updated, cmd = model.Update(cockpitRuneKey("t"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("returning to live mode/cmd = %v/%T, want live/tick command", model.mode, cmd)
	}
}

func TestCockpitModel_LiveDownToNewestResumesAutoFollow(t *testing.T) {
	t.Parallel()

	olderEvent := mustEvent(t, "evt-down-older", domtypes.EventKindNote, "older event")
	newerEvent := mustEvent(t, "evt-down-newer", domtypes.EventKindNote, "newer event")
	loader := &cockpitLoaderStub{}
	cockpit := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	cockpit.loader = loader
	cockpit.loaderCtx = context.Background()
	cockpit.live.events = []*model.Event{olderEvent, newerEvent}
	cockpit.live.cursor = newTailCursor(newerEvent.CreatedAt())
	cockpit.live.loadedAt = fixedStartedAt
	cockpit.live.selected = 0
	cockpit.live.follow = false
	cockpit.live.pausedNewCount = 1

	updated, cmd := cockpit.Update(tea.KeyMsg{Type: tea.KeyDown})
	cockpit = updated.(cockpitModel)
	if !cockpit.live.follow || cockpit.live.pausedNewCount != 0 || cmd == nil {
		t.Fatalf("down to newest follow/new/cmd = %v/%d/%T, want follow/0/tick", cockpit.live.follow, cockpit.live.pausedNewCount, cmd)
	}
	if got, want := cockpit.live.selected, 1; got != want {
		t.Fatalf("selected row after down = %d, want newest %d", got, want)
	}
	runFirstCockpitBatchCommandForTest(t, cmd)
	if got, want := len(loader.eventSeenCalls), 1; got != want {
		t.Fatalf("event seen calls after down resume = %d, want %d", got, want)
	}
}

func TestCockpitModel_LivePausedAppendTruncationKeepsFocusedRow(t *testing.T) {
	t.Parallel()

	events := make([]*model.Event, 0, cockpitLiveMaxEvents)
	base := fixedStartedAt.Add(-time.Hour)
	for i := range cockpitLiveMaxEvents {
		events = append(events, cockpitEventFixtureAt(t, fmt.Sprintf("evt-buffer-%03d", i), base.Add(time.Duration(i)*time.Second)))
	}
	newEvents := []*model.Event{}
	for i := range 5 {
		newEvents = append(newEvents, cockpitEventFixtureAt(t, fmt.Sprintf("evt-new-%03d", i), base.Add(time.Duration(cockpitLiveMaxEvents+i)*time.Second)))
	}
	cursor := newTailCursor(newEvents[len(newEvents)-1].CreatedAt())
	cursor.Advance(newEvents)

	for _, selected := range []int{0, 2, 50} {
		selected := selected
		t.Run(fmt.Sprintf("selected_%d", selected), func(t *testing.T) {
			t.Parallel()
			focused := events[selected]
			model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
			model.live.events = slices.Clone(events)
			model.live.selected = selected
			model.live.follow = false

			updated, _ := model.Update(cockpitLiveMsg{
				seq:      model.live.requestSeq,
				snapshot: cockpitLiveSnapshot{Events: newEvents, Cursor: cursor, LoadedAt: fixedStartedAt.Add(time.Minute)},
			})
			model = updated.(cockpitModel)
			if got, want := len(model.live.events), cockpitLiveMaxEvents; got != want {
				t.Fatalf("live events after truncate = %d, want %d", got, want)
			}
			if got := model.live.events[model.live.selected].EventID(); got != focused.EventID() {
				t.Fatalf("focused event after truncate = %s, want %s", got, focused.EventID())
			}
			if got, want := model.live.pausedNewCount, len(newEvents); got != want {
				t.Fatalf("paused new count after truncate append = %d, want %d", got, want)
			}
		})
	}
}

func TestCockpitModel_LiveLoadDoesNotMarkAfterLeavingLive(t *testing.T) {
	t.Parallel()

	event := mustEvent(t, "evt-leave-live", domtypes.EventKindNote, "leave live before load completes")
	loader := &cockpitLoaderStub{
		liveResponses: []cockpitLiveSnapshot{
			{Events: []*model.Event{event}, Cursor: newTailCursor(event.CreatedAt()), LoadedAt: fixedStartedAt},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()
	model.mode = cockpitModeTop

	updated, cmd := model.Update(cockpitRuneKey("t"))
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("opening live pane returned nil command")
	}
	updated, homeCmd := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeTop || homeCmd == nil {
		t.Fatalf("leaving live mode/cmd = %v/%T, want top/top load command", model.mode, homeCmd)
	}
	updated, markCmd := model.Update(cmd())
	model = updated.(cockpitModel)
	if markCmd != nil {
		t.Fatalf("stale live load returned mark command = %T, want nil after leaving live", markCmd)
	}
	if got := len(loader.eventSeenCalls); got != 0 {
		t.Fatalf("event seen calls after leaving live = %d, want 0", got)
	}
}

func TestCockpitModel_IgnoresStaleLiveLoadResponses(t *testing.T) {
	t.Parallel()

	staleEvent := mustEvent(t, "evt-stale", domtypes.EventKindNote, "stale live event")
	freshEvent := mustEvent(t, "evt-fresh", domtypes.EventKindNote, "fresh live event")
	cockpit := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	cockpit.loader = &cockpitLoaderStub{}
	cockpit.loaderCtx = context.Background()
	cockpit.mode = cockpitModeTop

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

func TestCockpitModel_MemoryReviewLaunchAndFinishRefreshesHome(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-review", "review this candidate", domtypes.MemoryStatusCandidate)
	accepted := cockpitMemoryDetailsFixture(t, "mem-review", "review this candidate", domtypes.MemoryStatusAccepted)
	loader := &cockpitLoaderStub{
		reviewItems: []apptypes.MemoryDetails{candidate},
		reviewFinishResult: memoryInboxReviewResult{
			Accepted: []apptypes.MemoryDetails{accepted},
		},
		homeResponses: []cockpitHomeSnapshot{
			{
				LoadedAt:                fixedStartedAt.Add(time.Minute),
				AcceptedMemoryCount:     1,
				CandidateMemoryCount:    0,
				NewCandidateMemoryKnown: true,
			},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{
		LoadedAt:             fixedStartedAt,
		CandidateMemoryCount: 1,
	})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeMemoryReview || !model.memoryReview.loading {
		t.Fatalf("memory review launch mode/loading = %v/%v, want memory review/loading", model.mode, model.memoryReview.loading)
	}
	if cmd == nil {
		t.Fatalf("memory review launch returned nil command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("memory review load should mark memory seen")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("memory seen marker returned message = %T, want nil", msg)
	}
	if got, want := len(loader.memorySeenCalls), 1; got != want {
		t.Fatalf("memory seen calls after review load = %d, want %d", got, want)
	}
	if got, want := loader.reviewLoadCalls, 1; got != want {
		t.Fatalf("review load calls = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "review this candidate") {
		t.Fatalf("memory review view missing candidate:\n%s", got)
	}

	updated, cmd = model.Update(cockpitRuneKey("a"))
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("accept key returned unexpected command = %T", cmd)
	}
	updated, cmd = model.Update(cockpitRuneKey("q"))
	model = updated.(cockpitModel)
	if !model.memoryReview.applying || cmd == nil {
		t.Fatalf("finish review applying/cmd = %v/%T, want applying/apply command", model.memoryReview.applying, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive {
		t.Fatalf("after apply mode = %v, want tail", model.mode)
	}
	if cmd == nil {
		t.Fatalf("after apply should refresh cockpit summary and tail")
	}
	if got, want := len(loader.reviewFinishCalls), 1; got != want {
		t.Fatalf("review finish decision count = %d, want %d", got, want)
	}
	if got := loader.reviewFinishCalls[0].kind; got != reviewDecisionAccept {
		t.Fatalf("review finish decision = %v, want accept", got)
	}

	model = applyCockpitImmediateCommandForTest(t, model, cmd)
	if got, want := loader.homeCalls, 1; got != want {
		t.Fatalf("home refresh calls = %d, want %d", got, want)
	}
	if got, want := len(loader.liveCalls), 1; got != want {
		t.Fatalf("tail refresh calls = %d, want %d", got, want)
	}
	if got := loader.liveCalls[0].initial; !got {
		t.Fatalf("tail refresh initial = %v, want true", got)
	}
	if got, want := model.home.CandidateMemoryCount, 0; got != want {
		t.Fatalf("refreshed CandidateMemoryCount = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "memory review applied: accepted=1 rejected=0 distilled=0 failures=0") {
		t.Fatalf("tail view missing review result:\n%s", got)
	}
}

func TestCockpitModel_MemoryReviewMarksLoadStartAsSeen(t *testing.T) {
	candidate := cockpitMemoryDetailsFixture(t, "mem-review-checkpoint", "review checkpoint candidate", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{candidate}}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{
		LoadedAt:             fixedStartedAt,
		CandidateMemoryCount: 1,
	})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("memory review launch returned nil command")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("memory review load should mark memory seen")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("memory seen marker returned message = %T, want nil", msg)
	}
	if got, want := len(loader.memorySeenCalls), 1; got != want {
		t.Fatalf("memory seen calls after review load = %d, want %d", got, want)
	}
	if seenAt := loader.memorySeenCalls[0]; seenAt.After(loader.reviewLoadStartedAt) {
		t.Fatalf("memory seen checkpoint = %v, want not after loader start %v", seenAt, loader.reviewLoadStartedAt)
	}
}

func TestCockpitModel_MemoryReviewOwnSectionReselectKeepsReviewState(t *testing.T) {
	t.Parallel()

	first := cockpitMemoryDetailsFixture(t, "mem-review-current-1", "first candidate", domtypes.MemoryStatusCandidate)
	second := cockpitMemoryDetailsFixture(t, "mem-review-current-2", "second candidate", domtypes.MemoryStatusCandidate)
	third := cockpitMemoryDetailsFixture(t, "mem-review-current-3", "third candidate", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{first, second, third}}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{
		LoadedAt:             fixedStartedAt,
		CandidateMemoryCount: 3,
	})
	model.loader = loader
	model.loaderCtx = context.Background()
	memorySectionKey := cockpitSectionRuneKeyForTest(t, cockpitSectionMemory)
	acceptKey := singleRuneReviewActionKeyMsgForTest(t, defaultReviewActionKeys().Accept.Keys())

	updated, cmd := model.Update(memorySectionKey)
	model = updated.(cockpitModel)
	if model.mode != cockpitModeMemoryReview || !model.memoryReview.loading || cmd == nil {
		t.Fatalf("memory launch mode/loading/cmd = %v/%v/%T, want memory/loading/cmd", model.mode, model.memoryReview.loading, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("memory review load returned nil seen-marker command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("memory seen marker returned message = %T, want nil", msg)
	}
	if model.memoryReview.review.mode != reviewModeBrowse {
		t.Fatalf("review mode after load = %v, want browse", model.memoryReview.review.mode)
	}
	if got, want := loader.reviewLoadCalls, 1; got != want {
		t.Fatalf("review load calls after launch = %d, want %d", got, want)
	}

	updated, cmd = model.Update(acceptKey)
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("accept key returned command = %T, want nil", cmd)
	}
	updated, cmd = model.Update(acceptKey)
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("second accept key returned command = %T, want nil", cmd)
	}
	assertMemoryReviewQueuedAccepts(t, "before reselect", model, first, second, third)

	updated, cmd = model.Update(memorySectionKey)
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("own memory section reselect returned command = %T, want nil to avoid reloading/resetting review state", cmd)
	}
	if model.mode != cockpitModeMemoryReview {
		t.Fatalf("mode after own memory section reselect = %v, want memory review", model.mode)
	}
	if model.memoryReview.loading || model.memoryReview.err != nil {
		t.Fatalf("memory review state after own section reselect loading/err = %v/%v, want false/nil", model.memoryReview.loading, model.memoryReview.err)
	}
	if got, want := loader.reviewLoadCalls, 1; got != want {
		t.Fatalf("review load calls after own memory section reselect = %d, want %d", got, want)
	}
	assertMemoryReviewQueuedAccepts(t, "after reselect", model, first, second, third)
}

func cockpitSectionRuneKeyForTest(t *testing.T, section cockpitSectionID) tea.KeyMsg {
	t.Helper()

	for _, navigationSection := range cockpitNavigationSectionsList() {
		if navigationSection.id == section {
			return cockpitRuneKey(navigationSection.key)
		}
	}
	t.Fatalf("cockpit section %v has no navigation key", section)
	return tea.KeyMsg{}
}

func singleRuneReviewActionKeyMsgForTest(t *testing.T, keys []string) tea.KeyMsg {
	t.Helper()

	if len(keys) == 0 {
		t.Fatalf("review action has no keys")
	}
	switch keys[0] {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	runes := []rune(keys[0])
	if len(runes) != 1 {
		t.Fatalf("review action key %q cannot be converted to a test KeyMsg", keys[0])
	}
	return cockpitRuneKey(keys[0])
}

func assertMemoryReviewQueuedAccepts(t *testing.T, phase string, model cockpitModel, first, second, third apptypes.MemoryDetails) {
	t.Helper()

	decisions := model.memoryReview.review.Decisions()
	if got, want := len(decisions), 2; got != want {
		t.Fatalf("%s queued decisions = %d, want %d", phase, got, want)
	}
	wants := []domtypes.MemoryID{first.Summary().MemoryID(), second.Summary().MemoryID()}
	for i, wantID := range wants {
		if decisions[i].kind != reviewDecisionAccept || decisions[i].memoryID != wantID {
			t.Fatalf("%s queued decision[%d] = %#v, want accept for %s", phase, i, decisions[i], wantID)
		}
	}
	if got, want := model.memoryReview.review.cursor, 2; got != want {
		t.Fatalf("%s review cursor = %d, want %d", phase, got, want)
	}
	if got, want := len(model.memoryReview.review.reviewed), 3; got != want {
		t.Fatalf("%s reviewed markers len = %d, want %d: %#v", phase, got, want, model.memoryReview.review.reviewed)
	}
	for i := 0; i < 2; i++ {
		if got, want := model.memoryReview.review.reviewed[i], decisionLabel(reviewDecisionAccept); got != want {
			t.Fatalf("%s reviewed[%d] = %q, want %q", phase, i, got, want)
		}
	}
	if got := model.memoryReview.review.reviewed[2]; got != "" {
		t.Fatalf("%s reviewed[2] = %q, want untouched", phase, got)
	}
	if model.memoryReview.review.cursor < 0 || model.memoryReview.review.cursor >= len(model.memoryReview.review.items) {
		t.Fatalf("%s review cursor = %d outside items len %d", phase, model.memoryReview.review.cursor, len(model.memoryReview.review.items))
	}
	current := model.memoryReview.review.items[model.memoryReview.review.cursor]
	if got, want := current.Summary().MemoryID(), third.Summary().MemoryID(); got != want {
		t.Fatalf("%s current candidate = %s, want %s", phase, got, want)
	}
}

func TestCockpitModel_MemoryReviewEscDismissesEvidenceWithoutApplying(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-evidence", "check evidence", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{candidate}}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("v"))
	model = updated.(cockpitModel)
	if model.memoryReview.review.mode != reviewModeViewEvidence {
		t.Fatalf("review mode = %v, want evidence", model.memoryReview.review.mode)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("esc from evidence returned command = %T, want nil", cmd)
	}
	if model.mode != cockpitModeMemoryReview || model.memoryReview.review.mode != reviewModeBrowse {
		t.Fatalf("esc from evidence mode/reviewMode = %v/%v, want memory review/browse", model.mode, model.memoryReview.review.mode)
	}
	if len(loader.reviewFinishCalls) != 0 {
		t.Fatalf("esc from evidence applied decisions unexpectedly: %#v", loader.reviewFinishCalls)
	}
}

func TestCockpitModel_MemoryReviewHelpKeepsGlobalShellDiscoverable(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")

	candidate := cockpitMemoryDetailsFixture(t, "mem-help", "show contextual and global help", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{candidate}}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("3"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)

	updated, cmd = model.Update(cockpitRuneKey("?"))
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("? from memory review returned command = %T, want nil", cmd)
	}
	if !model.showHelp || model.memoryReview.review.mode != reviewModeHelp {
		t.Fatalf("? help state = showHelp:%v reviewMode:%v, want global help + review help", model.showHelp, model.memoryReview.review.mode)
	}
	if got := model.View(); !strings.Contains(got, "Global navigation") || !strings.Contains(got, "memory review · help") {
		t.Fatalf("memory review help missing global/contextual help:\n%s", got)
	}

	updated, cmd = model.Update(cockpitRuneKey("?"))
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("second ? from memory review returned command = %T, want nil", cmd)
	}
	if model.showHelp || model.memoryReview.review.mode != reviewModeBrowse {
		t.Fatalf("second ? help state = showHelp:%v reviewMode:%v, want browse without global help", model.showHelp, model.memoryReview.review.mode)
	}
}

func TestCockpitModel_MemoryReviewEscBacksOutWithoutApplying(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-backout", "do not apply on escape", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{
		reviewItems: []apptypes.MemoryDetails{candidate},
		homeResponses: []cockpitHomeSnapshot{
			{
				LoadedAt:             fixedStartedAt.Add(time.Minute),
				CandidateMemoryCount: 1,
			},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("a"))
	model = updated.(cockpitModel)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive {
		t.Fatalf("esc from review browse mode = %v, want tail", model.mode)
	}
	if model.memoryReview.applying {
		t.Fatalf("esc from review browse started applying decisions")
	}
	if len(loader.reviewFinishCalls) != 0 {
		t.Fatalf("esc from review browse applied decisions unexpectedly: %#v", loader.reviewFinishCalls)
	}
	if cmd == nil {
		t.Fatalf("esc from review browse returned nil command, want tail refresh")
	}

	model = applyCockpitImmediateCommandForTest(t, model, cmd)
	if got, want := loader.homeCalls, 1; got != want {
		t.Fatalf("home refresh calls = %d, want %d", got, want)
	}
	if got, want := len(loader.liveCalls), 1; got != want {
		t.Fatalf("tail refresh calls = %d, want %d", got, want)
	}
}

func TestCockpitModel_MemoryReviewErrorAllowsHomeAndQuit(t *testing.T) {
	t.Parallel()

	home := cockpitHomeSnapshot{LoadedAt: fixedStartedAt}
	loader := &cockpitLoaderStub{
		homeResponses: []cockpitHomeSnapshot{
			{
				LoadedAt:             fixedStartedAt.Add(time.Minute),
				CandidateMemoryCount: 3,
			},
			{
				LoadedAt:             fixedStartedAt.Add(2 * time.Minute),
				CandidateMemoryCount: 4,
			},
		},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.loader = loader
	model.loaderCtx = context.Background()
	model.mode = cockpitModeMemoryReview
	model.memoryReview.err = context.Canceled

	updated, cmd := model.Update(cockpitRuneKey("h"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("h from review error mode/cmd = %v/%T, want tail/refresh command", model.mode, cmd)
	}
	model = applyCockpitImmediateCommandForTest(t, model, cmd)
	if got, want := model.home.CandidateMemoryCount, 3; got != want {
		t.Fatalf("home refresh after h CandidateMemoryCount = %d, want %d", got, want)
	}
	if got, want := len(loader.liveCalls), 1; got != want {
		t.Fatalf("tail refresh after h calls = %d, want %d", got, want)
	}

	model.mode = cockpitModeMemoryReview
	model.memoryReview.err = context.Canceled
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("esc from review error mode/cmd = %v/%T, want tail/refresh command", model.mode, cmd)
	}
	model = applyCockpitImmediateCommandForTest(t, model, cmd)
	if got, want := model.home.CandidateMemoryCount, 4; got != want {
		t.Fatalf("home refresh after esc CandidateMemoryCount = %d, want %d", got, want)
	}
	if got, want := len(loader.liveCalls), 2; got != want {
		t.Fatalf("tail refresh after esc calls = %d, want %d", got, want)
	}

	model.mode = cockpitModeMemoryReview
	model.memoryReview.err = context.Canceled
	_, cmd = model.Update(cockpitRuneKey("q"))
	if cmd == nil {
		t.Fatalf("q from review error returned nil command, want quit command")
	}
}

func TestCockpitModel_MemoryReviewLoadingEscBacksOut(t *testing.T) {
	t.Parallel()

	loader := &cockpitLoaderStub{}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()
	model.mode = cockpitModeMemoryReview
	model.memoryReview.loading = true
	model.memoryReview.requestSeq = 1

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("esc while review loading mode/cmd = %v/%T, want tail/refresh command", model.mode, cmd)
	}
	model = applyCockpitImmediateCommandForTest(t, model, cmd)
	if got, want := loader.homeCalls, 1; got != want {
		t.Fatalf("home refresh after loading esc calls = %d, want %d", got, want)
	}
	if got, want := len(loader.liveCalls), 1; got != want {
		t.Fatalf("tail refresh after loading esc calls = %d, want %d", got, want)
	}

	model.mode = cockpitModeMemoryReview
	model.memoryReview.loading = true
	_, cmd = model.Update(cockpitRuneKey("q"))
	if cmd == nil {
		t.Fatalf("q while review loading returned nil command, want quit command")
	}
}

func TestCockpitModel_MemoryReviewApplyFailureStaysInReview(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-fail-review", "review fails", domtypes.MemoryStatusCandidate)
	result := memoryInboxReviewResult{Failures: []memoryInboxFailure{{ID: "mem-fail-review", Error: "conflict"}}}
	loader := &cockpitLoaderStub{
		reviewItems:        []apptypes.MemoryDetails{candidate},
		reviewFinishResult: result,
		reviewFinishErr:    memoryReviewFailureError(result),
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("a"))
	model = updated.(cockpitModel)
	updated, cmd = model.Update(cockpitRuneKey("q"))
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("finish review returned nil command")
	}

	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("failed apply returned follow-up command = %T, want nil", cmd)
	}
	if model.mode != cockpitModeMemoryReview || model.memoryReview.err == nil {
		t.Fatalf("failed apply mode/err = %v/%v, want memory review/error", model.mode, model.memoryReview.err)
	}
	if got, want := len(model.memoryReview.result.Failures), 1; got != want {
		t.Fatalf("failed apply result failures = %d, want %d", got, want)
	}
}

func TestCockpitModel_MemoryReviewBlocksQuitWhileApplying(t *testing.T) {
	t.Parallel()

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.mode = cockpitModeMemoryReview
	model.memoryReview.applying = true

	_, cmd := model.Update(cockpitRuneKey("q"))
	if cmd != nil {
		t.Fatalf("q while review apply is in flight returned command = %T, want nil", cmd)
	}
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc while review apply is in flight returned command = %T, want nil", cmd)
	}
}

func TestCockpitModel_MemoryReviewCtrlCQuitsWithoutApplying(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-ctrlc-review", "do not apply on ctrl c", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{candidate}}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("a"))
	model = updated.(cockpitModel)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("ctrl+c during review returned nil command, want quit command")
	}
	if model.memoryReview.applying {
		t.Fatalf("ctrl+c during review started applying decisions")
	}
	if len(loader.reviewFinishCalls) != 0 {
		t.Fatalf("ctrl+c during review applied decisions unexpectedly: %#v", loader.reviewFinishCalls)
	}
}

func TestCockpitModel_IgnoresStaleMemoryReviewLoadResponses(t *testing.T) {
	t.Parallel()

	oldCandidate := cockpitMemoryDetailsFixture(t, "mem-old-review", "old review queue", domtypes.MemoryStatusCandidate)
	newCandidate := cockpitMemoryDetailsFixture(t, "mem-new-review", "new review queue", domtypes.MemoryStatusCandidate)
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})

	updated, _ := model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	firstSeq := model.memoryReview.requestSeq

	updated, _ = model.Update(cockpitRuneKey("h"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive {
		t.Fatalf("h while review loading mode = %v, want tail", model.mode)
	}

	updated, _ = model.Update(cockpitRuneKey("m"))
	model = updated.(cockpitModel)
	secondSeq := model.memoryReview.requestSeq
	if firstSeq == secondSeq {
		t.Fatalf("memory review request sequence did not advance: first=%d second=%d", firstSeq, secondSeq)
	}

	updated, _ = model.Update(cockpitMemoryReviewLoadedMsg{seq: firstSeq, items: []apptypes.MemoryDetails{oldCandidate}})
	model = updated.(cockpitModel)
	if !model.memoryReview.loading {
		t.Fatalf("stale review response cleared loading for the newer request")
	}
	if len(model.memoryReview.items) != 0 {
		t.Fatalf("stale review response mutated items: %#v", model.memoryReview.items)
	}

	updated, _ = model.Update(cockpitMemoryReviewLoadedMsg{seq: secondSeq, items: []apptypes.MemoryDetails{newCandidate}})
	model = updated.(cockpitModel)
	if model.memoryReview.loading {
		t.Fatalf("current review response left loading true")
	}
	if got, want := len(model.memoryReview.items), 1; got != want {
		t.Fatalf("current review items = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "new review queue") || strings.Contains(got, "old review queue") {
		t.Fatalf("review view did not use current queue only:\n%s", got)
	}
}

func TestCockpitModel_HomeRefreshErrorUsesErrorStatus(t *testing.T) {
	t.Parallel()

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitHomeMsg{err: context.Canceled})
	model = updated.(cockpitModel)
	if model.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want empty for refresh errors", model.statusMsg)
	}
	if model.statusErr == "" {
		t.Fatalf("statusErr = empty, want refresh error")
	}
	if got := model.View(); !strings.Contains(got, context.Canceled.Error()) {
		t.Fatalf("home view missing refresh error:\n%s", got)
	}
}

func TestCockpitModel_IgnoresStaleHomeRefreshResponses(t *testing.T) {
	t.Parallel()

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{
		LoadedAt:             fixedStartedAt,
		CandidateMemoryCount: 10,
	})
	_ = model.startCockpitHomeLoad()
	firstSeq := model.homeRequestSeq
	_ = model.startCockpitHomeLoad()
	secondSeq := model.homeRequestSeq
	if firstSeq == secondSeq {
		t.Fatalf("home request sequence did not advance: first=%d second=%d", firstSeq, secondSeq)
	}

	updated, _ := model.Update(cockpitHomeMsg{seq: firstSeq, err: context.Canceled})
	model = updated.(cockpitModel)
	if model.statusErr != "" {
		t.Fatalf("stale home error set statusErr = %q", model.statusErr)
	}
	if got, want := model.home.CandidateMemoryCount, 10; got != want {
		t.Fatalf("stale home error changed CandidateMemoryCount = %d, want %d", got, want)
	}

	updated, _ = model.Update(cockpitHomeMsg{seq: secondSeq, home: cockpitHomeSnapshot{
		LoadedAt:             fixedStartedAt.Add(time.Minute),
		CandidateMemoryCount: 2,
	}})
	model = updated.(cockpitModel)
	if got, want := model.home.CandidateMemoryCount, 2; got != want {
		t.Fatalf("current home response CandidateMemoryCount = %d, want %d", got, want)
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

func TestCockpitModel_GlobalNavigationShellRendersOnEveryScreen(t *testing.T) {
	t.Parallel()

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		NewCandidateMemoryKnown: true,
	})

	cases := []struct {
		name   string
		model  cockpitModel
		expect []string
	}{
		{
			name: "top",
			model: func() cockpitModel {
				next := model
				next.mode = cockpitModeTop
				return next
			}(),
			expect: []string{
				"Traceary cockpit · top",
				"tabs: 1 Tail  [2 Top]  3 Memory  4 Sessions  5 Settings",
				"Top summary",
			},
		},
		{
			name: "live",
			model: func() cockpitModel {
				next := model
				next.mode = cockpitModeLive
				next.live.loadedAt = fixedStartedAt
				return next
			}(),
			expect: []string{
				"Traceary cockpit · live tail",
				"tabs: [1 Tail]  2 Top  3 Memory  4 Sessions  5 Settings",
				"r refresh",
			},
		},
		{
			name: "detail",
			model: func() cockpitModel {
				next := model
				next.mode = cockpitModeDetail
				next.detail.title = "EVENT evt-shell"
				next.detail.lines = []string{"detail line"}
				return next
			}(),
			expect: []string{
				"Traceary cockpit · EVENT evt-shell",
				"tabs: [1 Tail]  2 Top  3 Memory  4 Sessions  5 Settings",
				"esc back",
			},
		},
		{
			name: "memory review",
			model: func() cockpitModel {
				next := model
				next.mode = cockpitModeMemoryReview
				next.memoryReview.loading = true
				return next
			}(),
			expect: []string{
				"Traceary cockpit · memory review",
				"tabs: 1 Tail  2 Top  [3 Memory]  4 Sessions  5 Settings",
				"Loading memory review queue",
			},
		},
		{
			name: "sessions",
			model: func() cockpitModel {
				next := model
				next.mode = cockpitModeSessions
				return next
			}(),
			expect: []string{
				"Traceary cockpit · sessions",
				"tabs: 1 Tail  2 Top  3 Memory  [4 Sessions]  5 Settings",
				"traceary session handoff",
			},
		},
		{
			name: "settings",
			model: func() cockpitModel {
				next := model
				next.mode = cockpitModeSettings
				next.settings = cockpitSettingsState{
					loaded: true,
					snapshot: cockpitSettingsSnapshot{
						Path:   "/tmp/config.json",
						Status: cockpitSettingsConfigMissing,
					},
				}
				next.settings.draft = next.settings.snapshot.Values.clone()
				return next
			}(),
			expect: []string{
				"Traceary cockpit · settings",
				"tabs: 1 Tail  2 Top  3 Memory  4 Sessions  [5 Settings]",
				"config status: missing",
				"tab/shift+tab next/prev",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := tc.model.View()
			for _, must := range tc.expect {
				if !strings.Contains(view, must) {
					t.Fatalf("%s view missing %q:\n%s", tc.name, must, view)
				}
			}
		})
	}
}

func TestCockpitModel_ContextualHelpActionMenuByScreen(t *testing.T) {
	t.Parallel()

	home := cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		NewCandidateMemoryKnown: true,
		CandidateMemoryCount:    1,
	}
	base := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	base.showHelp = true

	t.Run("tail", func(t *testing.T) {
		view := base.View()
		for _, must := range []string{
			"Action menu",
			"Refresh Tail events",
			"Jump to newest and resume auto-follow",
			"Global navigation",
			"1 Tail",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("tail help missing %q:\n%s", must, view)
			}
		}
		if strings.Contains(view, "1 Home") {
			t.Fatalf("tail help should not advertise Home:\n%s", view)
		}
	})

	t.Run("live empty", func(t *testing.T) {
		model := base
		model.mode = cockpitModeLive
		model.live.loadedAt = fixedStartedAt
		view := model.View()
		for _, must := range []string{
			"Action menu",
			"Refresh Tail events",
			"Jump to newest and resume auto-follow",
			"Loading live events...",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("live empty help missing %q:\n%s", must, view)
			}
		}
		for _, mustNot := range []string{"enter detail", "Open selected event detail"} {
			if strings.Contains(view, mustNot) {
				t.Fatalf("live empty help advertised unavailable %q:\n%s", mustNot, view)
			}
		}
	})

	t.Run("live with selection", func(t *testing.T) {
		event := mustEvent(t, "evt-help-live", domtypes.EventKindNote, "help live event")
		cockpit := base
		cockpit.mode = cockpitModeLive
		cockpit.live.loadedAt = fixedStartedAt
		cockpit.live.events = []*model.Event{event}
		view := cockpit.View()
		for _, must := range []string{
			"enter detail",
			"Open selected event detail",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("live selection help missing %q:\n%s", must, view)
			}
		}
	})

	t.Run("live refresh error with cached selection", func(t *testing.T) {
		event := mustEvent(t, "evt-help-live-error", domtypes.EventKindNote, "cached help live event")
		cockpit := base
		cockpit.mode = cockpitModeLive
		cockpit.live.loadedAt = fixedStartedAt
		cockpit.live.events = []*model.Event{event}
		cockpit.live.err = context.Canceled
		view := cockpit.View()
		for _, must := range []string{
			"enter detail",
			"Open selected event detail",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("live cached-error help missing %q:\n%s", must, view)
			}
		}
	})

	t.Run("doctor", func(t *testing.T) {
		model := base
		model.mode = cockpitModeDoctor
		model.doctor.snapshot = cockpitDoctorSnapshot{
			LoadedAt: fixedStartedAt,
			Summary:  doctorSummary{Warn: 1},
			Sections: []cockpitDoctorSection{{Name: "Hooks", Checks: []cockpitDoctorCheck{
				{Name: "codex-config", Status: doctorStatusWarn, Severity: doctorSeverityWarn, Message: "missing hook", FixCommand: "traceary hooks install --client codex"},
			}}},
		}
		view := model.View()
		for _, must := range []string{
			"Refresh doctor checks",
			"Remediation commands are shown inline",
			"traceary hooks install --client codex",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("doctor help missing %q:\n%s", must, view)
			}
		}
	})

	t.Run("doctor error with stale remediation snapshot", func(t *testing.T) {
		model := base
		model.mode = cockpitModeDoctor
		model.doctor.err = context.Canceled
		model.doctor.snapshot = cockpitDoctorSnapshot{
			LoadedAt: fixedStartedAt,
			Summary:  doctorSummary{Warn: 1},
			Sections: []cockpitDoctorSection{{Name: "Hooks", Checks: []cockpitDoctorCheck{
				{Name: "codex-config", Status: doctorStatusWarn, Severity: doctorSeverityWarn, Message: "missing hook", FixCommand: "traceary hooks install --client codex"},
			}}},
		}
		view := model.View()
		if !strings.Contains(view, "Refresh doctor checks") {
			t.Fatalf("doctor error help missing refresh action:\n%s", view)
		}
		if strings.Contains(view, "Remediation commands are shown inline") {
			t.Fatalf("doctor error help advertised stale remediation:\n%s", view)
		}
	})

	t.Run("memory", func(t *testing.T) {
		candidate := cockpitMemoryDetailsFixture(t, "mem-help-menu", "review from contextual menu", domtypes.MemoryStatusCandidate)
		model := base
		model.mode = cockpitModeMemoryReview
		model.memoryReview.items = []apptypes.MemoryDetails{candidate}
		model.memoryReview.review = newReviewModel(model.memoryReview.items, model.keys, model.styles)
		view := model.View()
		for _, must := range []string{
			"Accept as-is only when the checklist passes",
			"Reject current candidate",
			"Skip when more context is needed",
			"Edit/distill into an operator-authored fact when wording is unclear",
			"View evidence and artifact refs",
			"Accept checklist: factual, stable, useful later",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("memory help missing %q:\n%s", must, view)
			}
		}
	})

	t.Run("settings", func(t *testing.T) {
		model := base
		model.mode = cockpitModeSettings
		model.settings = cockpitSettingsState{
			loaded: true,
			snapshot: cockpitSettingsSnapshot{
				Path:   "/tmp/config.json",
				Status: cockpitSettingsConfigMissing,
			},
		}
		model.settings.draft = model.settings.snapshot.Values.clone()
		view := model.View()
		for _, must := range []string{
			"Run the selected settings action or stage the selected value change",
			"tab / shift+tab",
			"← / → cycle tabs",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("settings help missing %q:\n%s", must, view)
			}
		}
		if strings.Contains(view, "← / → edit selected value rows") {
			t.Fatalf("settings help should not advertise ←/→ as value editing:\n%s", view)
		}
	})

	t.Run("settings modal help", func(t *testing.T) {
		model := base
		model.mode = cockpitModeSettings
		model.showHelp = true
		model.settings = cockpitSettingsState{
			loaded: true,
			snapshot: cockpitSettingsSnapshot{
				Path:   "/tmp/config.json",
				Status: cockpitSettingsConfigMissing,
			},
		}
		model.settings.draft = model.settings.snapshot.Values.clone()

		updated, _ := model.Update(cockpitRuneKey("n"))
		model = updated.(cockpitModel)
		view := model.View()
		if !strings.Contains(view, "Navigation is paused while regex input is active") {
			t.Fatalf("settings regex modal help missing paused-navigation copy:\n%s", view)
		}
		for _, mustNot := range []string{"\n1 Tail      ", "← / → cycle tabs", "← / → edit selected value rows"} {
			if strings.Contains(view, mustNot) {
				t.Fatalf("settings regex modal help advertised unavailable navigation %q:\n%s", mustNot, view)
			}
		}

		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		model = updated.(cockpitModel)
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(cockpitModel)
		updated, _ = model.Update(cockpitRuneKey("w"))
		model = updated.(cockpitModel)
		view = model.View()
		if !strings.Contains(view, "Navigation is paused while config write confirmation is active") {
			t.Fatalf("settings save-confirmation help missing paused-navigation copy:\n%s", view)
		}
		for _, mustNot := range []string{"\n1 Tail      ", "← / → cycle tabs", "← / → edit selected value rows"} {
			if strings.Contains(view, mustNot) {
				t.Fatalf("settings confirmation help advertised unavailable navigation %q:\n%s", mustNot, view)
			}
		}
	})
}

func TestCockpitModel_LocaleSpecificScrollCopy(t *testing.T) {
	tests := []struct {
		locale         string
		want           string
		ignoreCase     bool
		forbidEnglish  bool
		forbidJapanese bool
	}{
		{locale: "en", want: "scroll", ignoreCase: true, forbidJapanese: true},
		{locale: "ja", want: "スクロール", forbidEnglish: true},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			resetConfiguredCLILanguageCacheForTest()
			t.Cleanup(resetConfiguredCLILanguageCacheForTest)
			t.Setenv(cliLanguageEnvKey, tt.locale)

			for _, tc := range cockpitScrollCopyCases() {
				t.Run(tc.name, func(t *testing.T) {
					help := tc.help
					if tt.ignoreCase {
						help = strings.ToLower(help)
					}
					if !strings.Contains(help, tt.want) {
						t.Fatalf("%s help = %q, want %q", tc.name, tc.help, tt.want)
					}
					if tt.forbidJapanese && containsJapaneseScript(tc.help) {
						t.Fatalf("%s help leaked Japanese copy in English locale: %q", tc.name, tc.help)
					}
					if tt.forbidEnglish && strings.Contains(strings.ToLower(tc.help), "scroll") {
						t.Fatalf("%s help leaked English copy in Japanese locale: %q", tc.name, tc.help)
					}
				})
			}
		})
	}
}

func cockpitScrollCopyCases() []struct {
	name string
	help string
} {
	base := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	doctorSnapshot := cockpitDoctorSnapshot{
		LoadedAt: fixedStartedAt,
		Sections: []cockpitDoctorSection{{Name: "Checks", Checks: []cockpitDoctorCheck{
			{Name: "first", Status: doctorStatusPass, Message: "ok"},
			{Name: "second", Status: doctorStatusPass, Message: "ok"},
		}}},
	}
	return []struct {
		name string
		help string
	}{
		{
			name: "top detail",
			help: func() string {
				model := base
				model.mode = cockpitModeTop
				model.top.detailOpen = true
				model.top.detail.lines = []string{"first line", "second line"}
				return model.topLocalHelp()
			}(),
		},
		{
			name: "top detail action",
			help: func() string {
				model := base
				model.mode = cockpitModeTop
				model.top.detailOpen = true
				model.top.detail.lines = []string{"first line", "second line"}
				return cockpitActionDescriptions(model.cockpitContextualActions())
			}(),
		},
		{
			name: "doctor",
			help: func() string {
				model := base
				model.mode = cockpitModeDoctor
				model.doctor.snapshot = doctorSnapshot
				return model.doctorLocalHelp()
			}(),
		},
		{
			name: "doctor action",
			help: func() string {
				model := base
				model.mode = cockpitModeDoctor
				model.doctor.snapshot = doctorSnapshot
				return cockpitActionDescriptions(model.cockpitContextualActions())
			}(),
		},
		{
			name: "tail detail",
			help: func() string {
				model := base
				model.mode = cockpitModeDetail
				model.detail.lines = []string{"first line", "second line"}
				return model.detailLocalHelp()
			}(),
		},
	}
}

func cockpitActionDescriptions(actions []cockpitAction) string {
	descriptions := make([]string, 0, len(actions))
	for _, action := range actions {
		descriptions = append(descriptions, action.description)
	}
	return strings.Join(descriptions, "\n")
}

func TestCockpitModel_NavigationLinesAlignByDisplayWidth(t *testing.T) {
	tests := []struct {
		locale       string
		descriptions []string
		wantWidth    int
	}{
		{
			locale:    "en",
			wantWidth: 11,
			descriptions: []string{
				"live event stream and event details",
				"dashboard for sessions, failures, commands, memory, and health",
				"memory review queue",
				"session and handoff entry points",
				"language, read defaults, redaction diagnostics",
			},
		},
		{
			locale:    "ja",
			wantWidth: 13,
			descriptions: []string{
				"イベントのライブ表示と詳細確認",
				"セッション・失敗・コマンド・メモリ・状態の一覧",
				"メモリ候補の確認キュー",
				"セッション一覧と引き継ぎ導線",
				"言語・表示既定・redaction 診断",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			resetConfiguredCLILanguageCacheForTest()
			t.Cleanup(resetConfiguredCLILanguageCacheForTest)
			t.Setenv(cliLanguageEnvKey, tt.locale)

			model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
			lines := model.cockpitContextualNavigationLines()
			if got := cockpitNavigationLabelWidth(cockpitNavigationSectionsList()); got != tt.wantWidth {
				t.Fatalf("navigation label width = %d, want %d", got, tt.wantWidth)
			}
			for i, description := range tt.descriptions {
				line := lines[i]
				if !strings.HasSuffix(line, description) {
					t.Fatalf("navigation line %d missing description %q: %q", i+1, description, line)
				}
				prefix := strings.TrimSuffix(line, description)
				if got := runeWidth(prefix); got != tt.wantWidth {
					t.Fatalf("navigation line %d prefix display width = %d, want %d: %q", i+1, got, tt.wantWidth, line)
				}
				if got := runeWidth(strings.TrimRight(prefix, " ")); got >= tt.wantWidth {
					t.Fatalf("navigation line %d label width = %d, want room for a separator: %q", i+1, got, line)
				}
			}
		})
	}
}

func TestCockpitNavigationSectionsCoverKnownSectionIDs(t *testing.T) {
	t.Parallel()

	expected := []struct {
		id                  cockpitSectionID
		key                 string
		englishLabel        string
		japaneseLabel       string
		englishDescription  string
		japaneseDescription string
	}{
		{cockpitSectionLive, "1", "Tail", "Tail", "live event stream and event details", "イベントのライブ表示と詳細確認"},
		{cockpitSectionTop, "2", "Top", "Top", "dashboard for sessions, failures, commands, memory, and health", "セッション・失敗・コマンド・メモリ・状態の一覧"},
		{cockpitSectionMemory, "3", "Memory", "メモリ", "memory review queue", "メモリ候補の確認キュー"},
		{cockpitSectionSessions, "4", "Sessions", "セッション", "session and handoff entry points", "セッション一覧と引き継ぎ導線"},
		{cockpitSectionSettings, "5", "Settings", "設定", "language, read defaults, redaction diagnostics", "言語・表示既定・redaction 診断"},
	}
	if len(expected) != int(cockpitSectionCount) {
		t.Fatalf("expected navigation section count = %d, want cockpitSectionCount %d", len(expected), cockpitSectionCount)
	}
	sections := cockpitNavigationSectionsList()
	if got, want := len(sections), len(expected); got != want {
		t.Fatalf("navigation section count = %d, want %d", got, want)
	}
	seen := map[cockpitSectionID]struct{}{}
	seenKeys := map[string]struct{}{}
	byID := map[cockpitSectionID]cockpitNavigationSection{}
	for i, section := range sections {
		if section.id != cockpitSectionID(i) {
			t.Fatalf("navigation section %d id = %v, want %v", i, section.id, cockpitSectionID(i))
		}
		want := expected[i]
		if section.id != want.id || section.key != want.key || section.englishLabel != want.englishLabel || section.japaneseLabel != want.japaneseLabel || section.englishDescription != want.englishDescription || section.japaneseDescription != want.japaneseDescription {
			t.Fatalf("navigation section %d = %#v, want %#v", i, section, want)
		}
		if section.key == "" || section.englishLabel == "" || section.japaneseLabel == "" || section.englishDescription == "" || section.japaneseDescription == "" {
			t.Fatalf("navigation section has incomplete metadata: %#v", section)
		}
		if _, ok := seenKeys[section.key]; ok {
			t.Fatalf("duplicate navigation section key: %q", section.key)
		}
		seenKeys[section.key] = struct{}{}
		if _, ok := seen[section.id]; ok {
			t.Fatalf("duplicate navigation section id: %v", section.id)
		}
		seen[section.id] = struct{}{}
		byID[section.id] = section
	}
	for _, want := range expected {
		if _, ok := seen[want.id]; !ok {
			t.Fatalf("navigation section id %v is missing from cockpitNavigationSections", want.id)
		}
		section, ok := byID[want.id]
		if !ok {
			t.Fatalf("navigation section id %v missing from lookup", want.id)
		}
		if got := section.label(); got == "" {
			t.Fatalf("navigation section id %v has empty localized label", want.id)
		}
	}
}

func TestMemoryReviewWorkflowTitleLocalizesSuffixes(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		suffix string
		want   string
	}{
		{name: "english suffix", locale: "en", suffix: "help", want: "memory review · help"},
		{name: "japanese suffix", locale: "ja", suffix: "ヘルプ", want: "メモリ確認 · ヘルプ"},
		{name: "empty english suffix", locale: "en", want: "memory review"},
		{name: "empty japanese suffix", locale: "ja", want: "メモリ確認"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfiguredCLILanguageCacheForTest()
			t.Cleanup(resetConfiguredCLILanguageCacheForTest)
			t.Setenv(cliLanguageEnvKey, tt.locale)

			if got := memoryReviewWorkflowTitle(tt.suffix); got != tt.want {
				t.Fatalf("memoryReviewWorkflowTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCockpitModel_JapaneseMemoryReviewGlossary(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)
	t.Setenv(cliLanguageEnvKey, "ja")

	candidate := buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "mem-ja-glossary",
		fact:       "glossary candidate",
		confidence: domtypes.ConfidenceLow,
		source:     domtypes.MemorySourceExtractedHidden,
	})
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.mode = cockpitModeMemoryReview
	model.showHelp = true
	model.memoryReview.items = []apptypes.MemoryDetails{candidate}
	model.memoryReview.review = newReviewModel(model.memoryReview.items, model.keys, model.styles)
	updated, cmd := model.Update(cockpitRuneKey("a"))
	if cmd != nil {
		t.Fatalf("weak-candidate confirmation returned command = %T, want nil", cmd)
	}
	model = updated.(cockpitModel)

	view := model.View()
	for _, must := range []string{
		"Traceary cockpit · メモリ確認",
		"メモリ確認 · 判断カード",
		"メモリ候補 1 / 1",
		"メモリ候補の確認キュー",
		"メモリ候補 fact:",
		"この弱いメモリ候補",
		"現在のメモリ候補を reject",
		"evidence と artifact refs を表示",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("Japanese memory review glossary missing %q:\n%s", must, view)
		}
	}
	for _, mustNot := range []string{
		"inbox review ·",
		"candidate 1 / 1",
		"現在の候補を reject",
		"この弱い候補",
		"\n候補 fact:",
	} {
		if strings.Contains(view, mustNot) {
			t.Fatalf("Japanese memory review glossary leaked %q:\n%s", mustNot, view)
		}
	}

	empty := newReviewModel(nil, tui.DefaultKeyMap(), tui.DefaultStyles()).View()
	if !strings.Contains(empty, "メモリ候補の確認キューは空です") {
		t.Fatalf("empty memory review glossary missing canonical queue label:\n%s", empty)
	}
	if strings.Contains(empty, "メモリ確認キュー") || strings.Contains(empty, "inbox review") {
		t.Fatalf("empty memory review glossary leaked old queue term:\n%s", empty)
	}

	stdout := &bytes.Buffer{}
	rootCmd := NewRootCLI().Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"memory", "inbox", "review", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("memory inbox review --help: %v", err)
	}
	help := stdout.String()
	for _, must := range []string{
		"メモリ候補の確認キュー",
		"extracted-hidden のメモリ候補",
	} {
		if !strings.Contains(help, must) {
			t.Fatalf("Japanese memory inbox review help missing %q:\n%s", must, help)
		}
	}
}

func containsJapaneseScript(value string) bool {
	for _, r := range value {
		// Fullwidth Latin/digit/punctuation and halfwidth katakana code points are treated as Japanese-locale leaks on purpose.
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) ||
			(r >= 0x3000 && r <= 0x303f) ||
			(r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

func TestCockpitSettingsViewShowsConfigStatusAndEnvOverrides(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(cliLanguageEnvKey, "en")
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{
		"ui": {"language": "ja"},
		"read": {
			"color": "never",
			"fields": ["ts", "kind", "message"],
			"presets": {"mine": {"fields": ["ts"], "filters": {"kind": "prompt"}}}
		},
		"redact": {
			"extra_patterns": ["SECRET-[0-9]+"],
			"rules": [{"name": "token", "type": "regex"}]
		}
	}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, cmd := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("opening settings returned cmd = %T, want nil", cmd)
	}
	view := model.View()
	for _, must := range []string{
		"Traceary cockpit · settings",
		"config status: loaded",
		"TRACEARY_LANG=en overrides ui.language",
		"ui.language: ja",
		"read.color: never",
		"read.fields: ts,kind,message",
		"redact.extra_patterns add: SECRET-[0-9]+",
		"TRACEARY_DB_PATH=unset",
		"read.presets (view only in this release)",
		"mine fields=ts filters=kind=prompt",
		"redact.rules (view only in this release)",
		"token(regex)",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("settings view missing %q:\n%s", must, view)
		}
	}
}

func TestCockpitSettingsRowsMatchDispatchConstants(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)
	t.Setenv(cliLanguageEnvKey, "en")

	rows := cockpitSettingsState{}.settingsRows()
	if got, want := len(rows), cockpitSettingsRowCount; got != want {
		t.Fatalf("settings row count = %d, want %d", got, want)
	}
	if got := rows[cockpitSettingsRowLanguage]; !strings.Contains(got, "ui.language") {
		t.Fatalf("language row = %q, want ui.language marker", got)
	}
	if got := rows[cockpitSettingsRowReadColor]; !strings.Contains(got, "read.color") {
		t.Fatalf("read color row = %q, want read.color marker", got)
	}
	if got := rows[cockpitSettingsRowReadFields]; !strings.Contains(got, "read.fields") {
		t.Fatalf("read fields row = %q, want read.fields marker", got)
	}
	if got := rows[cockpitSettingsRowAddPattern]; !strings.Contains(got, "redact.extra_patterns add") {
		t.Fatalf("add pattern row = %q, want redact.extra_patterns add marker", got)
	}
	if got := rows[cockpitSettingsRowRemovePattern]; !strings.Contains(got, "redact.extra_patterns remove last") {
		t.Fatalf("remove pattern row = %q, want redact.extra_patterns remove marker", got)
	}
}

func TestCockpitSettingsStagesValidatesAndSavesConfigAtomically(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	previousLang, hadLang := os.LookupEnv(cliLanguageEnvKey)
	if err := os.Unsetenv(cliLanguageEnvKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadLang {
			_ = os.Setenv(cliLanguageEnvKey, previousLang)
		} else {
			_ = os.Unsetenv(cliLanguageEnvKey)
		}
	})
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)

	updated, _ = model.Update(cockpitRuneKey("l"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("c"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("f"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("n"))
	model = updated.(cockpitModel)
	for _, r := range "SECRET-[" {
		updated, _ = model.Update(cockpitRuneKey(string(r)))
		model = updated.(cockpitModel)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if !strings.Contains(model.View(), "Invalid regex") {
		t.Fatalf("invalid regex should be rejected before save:\n%s", model.View())
	}
	configPath := filepath.Join(home, ".config", "traceary", "config.json")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("invalid regex should not create config, stat err=%v", err)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("n"))
	model = updated.(cockpitModel)
	for _, r := range `SECRET-[0-9]+` {
		updated, _ = model.Update(cockpitRuneKey(string(r)))
		model = updated.(cockpitModel)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("w"))
	model = updated.(cockpitModel)
	if !strings.Contains(model.View(), "Confirm config write") || !strings.Contains(model.View(), "redact.extra_patterns") {
		t.Fatalf("settings save should require confirmation with diff:\n%s", model.View())
	}
	updated, _ = model.Update(cockpitRuneKey("y"))
	model = updated.(cockpitModel)

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved config invalid JSON: %v\n%s", err, data)
	}
	ui := settingsJSONSection(t, got, "ui")
	read := settingsJSONSection(t, got, "read")
	redact := settingsJSONSection(t, got, "redact")
	if ui["language"] != "ja" {
		t.Fatalf("ui.language = %v, want ja", ui["language"])
	}
	if read["color"] != "always" {
		t.Fatalf("read.color = %v, want always", read["color"])
	}
	fields := settingsJSONArray(t, read, "fields")
	if len(fields) == 0 {
		t.Fatalf("read.fields not saved: %#v", fields)
	}
	if gotPatterns := settingsJSONArray(t, redact, "extra_patterns"); len(gotPatterns) != 1 || gotPatterns[0] != "SECRET-[0-9]+" {
		t.Fatalf("redact.extra_patterns = %#v, want valid staged regex", gotPatterns)
	}
	if !strings.Contains(model.View(), "設定") {
		t.Fatalf("saved ui.language should refresh current cockpit language when TRACEARY_LANG is unset:\n%s", model.View())
	}
}

func TestCockpitSettingsArrowKeyWorkflowStagesAndConfirmsSave(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(cliLanguageEnvKey, "en")

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)

	// Row 0: ui.language, Row 1: read.color, Row 2: read.fields.
	for _, keyMsg := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
	} {
		updated, _ = model.Update(keyMsg)
		model = updated.(cockpitModel)
	}
	if !model.settings.editingPattern {
		t.Fatalf("enter on add-pattern row should open regex editor:\n%s", model.View())
	}
	for _, r := range `TOKEN-[A-Z]+` {
		updated, _ = model.Update(cockpitRuneKey(string(r)))
		model = updated.(cockpitModel)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	for range 2 {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(cockpitModel)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if !model.settings.confirmSave || !strings.Contains(model.View(), "Confirm config write") {
		t.Fatalf("enter on save row should require explicit confirmation:\n%s", model.View())
	}
	updated, _ = model.Update(cockpitRuneKey("y"))
	model = updated.(cockpitModel)

	configPath := filepath.Join(home, ".config", "traceary", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved config invalid JSON: %v\n%s", err, data)
	}
	ui := settingsJSONSection(t, got, "ui")
	read := settingsJSONSection(t, got, "read")
	redact := settingsJSONSection(t, got, "redact")
	if ui["language"] != "ja" {
		t.Fatalf("ui.language = %v, want ja", ui["language"])
	}
	if read["color"] != "always" {
		t.Fatalf("read.color = %v, want always", read["color"])
	}
	fields := settingsJSONArray(t, read, "fields")
	wantFields := []any{"ts", "kind", "exit_code", "session", "ws", "message"}
	if !slices.Equal(fields, wantFields) {
		t.Fatalf("read.fields = %#v, want first non-default preset %#v", fields, wantFields)
	}
	if gotPatterns := settingsJSONArray(t, redact, "extra_patterns"); len(gotPatterns) != 1 || gotPatterns[0] != "TOKEN-[A-Z]+" {
		t.Fatalf("redact.extra_patterns = %#v, want valid staged regex", gotPatterns)
	}
	view := model.View()
	if !strings.Contains(view, "TRACEARY_LANG=en") {
		t.Fatalf("TRACEARY_LANG override message should remain visible after save:\n%s", view)
	}
}

func TestCockpitSettingsLeftRightMoveTabsWithoutStagingValues(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)

	for _, row := range []int{cockpitSettingsRowLanguage, cockpitSettingsRowReadColor, cockpitSettingsRowReadFields} {
		model.settings.cursor = row
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRight})
		next := updated.(cockpitModel)
		if next.mode != cockpitModeLive || cmd == nil {
			t.Fatalf("right from settings row %d mode/cmd = %v/%T, want tail/load", row, next.mode, cmd)
		}
		if next.settings.dirty() {
			t.Fatalf("right from settings row %d staged values unexpectedly:\n%s", row, next.View())
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeSessions {
		t.Fatalf("left from settings mode = %v, want sessions", model.mode)
	}
	if model.settings.dirty() {
		t.Fatalf("left from settings staged values unexpectedly:\n%s", model.View())
	}
}

func TestCockpitSettingsEnterStagesEditableRows(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, row := range []int{cockpitSettingsRowLanguage, cockpitSettingsRowReadColor, cockpitSettingsRowReadFields} {
		model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
		updated, _ := model.Update(cockpitRuneKey("5"))
		model = updated.(cockpitModel)
		model.settings.cursor = row

		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(cockpitModel)
		if model.mode != cockpitModeSettings || cmd != nil || !model.settings.dirty() {
			t.Fatalf("row %d enter mode/cmd/dirty = %v/%T/%v, want settings/nil/dirty", row, model.mode, cmd, model.settings.dirty())
		}
	}
}

func TestCockpitSettingsDefaultCyclesAreSemanticallyReversible(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)

	for _, step := range []struct {
		name        string
		row         int
		cycles      int
		defaultText string
	}{
		{name: "ui.language", row: cockpitSettingsRowLanguage, cycles: 2, defaultText: "ui.language: en (default)"},
		{name: "read.color", row: cockpitSettingsRowReadColor, cycles: 3, defaultText: "read.color: auto (default)"},
		{name: "read.fields", row: cockpitSettingsRowReadFields, cycles: 4, defaultText: "read.fields: ts,kind,agent,session,ws,message (default)"},
	} {
		t.Run(step.name, func(t *testing.T) {
			cycleModel := model
			cycleModel.settings.cursor = step.row
			updated, _ := cycleModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
			cycleModel = updated.(cockpitModel)
			if !cycleModel.settings.dirty() {
				t.Fatalf("%s enter should stage a non-default value", step.name)
			}
			for range step.cycles - 1 {
				updated, _ = cycleModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
				cycleModel = updated.(cockpitModel)
			}
			if cycleModel.settings.dirty() {
				t.Fatalf("%s repeated enter should return to the semantic default:\n%s", step.name, cycleModel.View())
			}
			view := cycleModel.View()
			if !strings.Contains(view, step.defaultText) || strings.Contains(view, "Staged") {
				t.Fatalf("%s repeated enter should restore default display and clear staged info:\n%s", step.name, view)
			}
		})
	}
}

func TestSettingsFieldSetAtOffsetCustomSetRespectsDirection(t *testing.T) {
	custom := []string{"ts", "kind", "custom"}
	if got, want := settingsFieldSetAtOffset(custom, 1), readFieldIDsToStrings(defaultReadFields); !slices.Equal(got, want) {
		t.Fatalf("right from custom read.fields = %#v, want default %#v", got, want)
	}
	if got, want := settingsFieldSetAtOffset(custom, -1), []string{"ts", "kind", "message"}; !slices.Equal(got, want) {
		t.Fatalf("left from custom read.fields = %#v, want final preset %#v", got, want)
	}
}

func TestCockpitSettingsEnterActionsRemoveDiscardAndReload(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"ui":{"language":"en"},"redact":{"extra_patterns":["SECRET","TOKEN"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)

	model.settings.cursor = cockpitSettingsRowRemovePattern
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if got, want := model.settings.draft.ExtraPatterns, []string{"SECRET"}; !slices.Equal(got, want) {
		t.Fatalf("enter remove pattern = %#v, want %#v", got, want)
	}

	model.settings.cursor = cockpitSettingsRowDiscard
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if got, want := model.settings.draft.ExtraPatterns, []string{"SECRET", "TOKEN"}; !slices.Equal(got, want) {
		t.Fatalf("enter discard = %#v, want %#v", got, want)
	}

	if err := os.WriteFile(configPath, []byte(`{"ui":{"language":"ja"},"redact":{"extra_patterns":["UPDATED"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	model.settings.cursor = cockpitSettingsRowReload
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if got, want := model.settings.draft.UILanguage, "ja"; got != want {
		t.Fatalf("enter reload ui.language = %q, want %q", got, want)
	}
	if got, want := model.settings.draft.ExtraPatterns, []string{"UPDATED"}; !slices.Equal(got, want) {
		t.Fatalf("enter reload redact.extra_patterns = %#v, want %#v", got, want)
	}
}

func settingsJSONSection(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	raw, ok := doc[name]
	if !ok {
		t.Fatalf("saved config missing %q section: %#v", name, doc)
	}
	section, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("saved config %q section type = %T, want object: %#v", name, raw, raw)
	}
	return section
}

func settingsJSONArray(t *testing.T, section map[string]any, name string) []any {
	t.Helper()
	raw, ok := section[name]
	if !ok {
		t.Fatalf("saved config missing %q field: %#v", name, section)
	}
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("saved config %q field type = %T, want array: %#v", name, raw, raw)
	}
	return values
}

func TestCockpitSettingsInvalidConfigIsRecoverableAndNotOverwritten(t *testing.T) {
	resetConfiguredCLILanguageCacheForTest()
	t.Cleanup(resetConfiguredCLILanguageCacheForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "traceary")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{invalid}"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	updated, _ := model.Update(cockpitRuneKey("5"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("l"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("w"))
	model = updated.(cockpitModel)

	view := model.View()
	for _, must := range []string{"config status: invalid JSON", "Config edits are disabled", "Fix config readability/JSON"} {
		if !strings.Contains(view, must) {
			t.Fatalf("invalid config settings view missing %q:\n%s", must, view)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{invalid}" {
		t.Fatalf("invalid config was overwritten: %q", string(data))
	}
}

func TestCockpitSettingsSaveHandlesNullSectionsAndPreservesUnknownKeys(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	initial := `{"ui": null, "read": null, "redact": null, "unknown": {"keep": true}}`
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot := cockpitSettingsSnapshot{
		Path:   configPath,
		Status: cockpitSettingsConfigLoaded,
		Values: cockpitSettingsValues{
			UILanguage:    "",
			ReadColor:     "",
			ReadFields:    nil,
			ExtraPatterns: nil,
		},
	}
	values := snapshot.Values.clone()
	values.UILanguage = "ja"
	values.ReadColor = "never"
	values.ReadFields = []string{"ts", "kind", "message"}
	values.ExtraPatterns = []string{"SECRET-[0-9]+"}

	if err := saveCockpitSettingsDraft(snapshot, values); err != nil {
		t.Fatalf("saveCockpitSettingsDraft() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved config invalid JSON: %v\n%s", err, data)
	}
	if got["unknown"].(map[string]any)["keep"] != true {
		t.Fatalf("unknown key not preserved: %s", data)
	}
	if got["ui"].(map[string]any)["language"] != "ja" {
		t.Fatalf("ui section not replaced from null: %s", data)
	}
	if got["read"].(map[string]any)["color"] != "never" {
		t.Fatalf("read section not replaced from null: %s", data)
	}
	if gotPatterns := got["redact"].(map[string]any)["extra_patterns"].([]any); len(gotPatterns) != 1 || gotPatterns[0] != "SECRET-[0-9]+" {
		t.Fatalf("redact section not replaced from null: %s", data)
	}
}

func TestCockpitSettingsLanguageStagedMarkerUsesNormalizedDiff(t *testing.T) {
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.mode = cockpitModeSettings
	model.settings = cockpitSettingsState{
		loaded: true,
		snapshot: cockpitSettingsSnapshot{
			Path:   filepath.Join(t.TempDir(), "config.json"),
			Status: cockpitSettingsConfigLoaded,
			Values: cockpitSettingsValues{
				UILanguage: "ja-JP",
			},
		},
	}
	model.settings.draft = model.settings.snapshot.Values.clone()

	updated, _ := model.Update(cockpitRuneKey("l"))
	model = updated.(cockpitModel)
	if !model.settings.dirty() || !strings.Contains(model.View(), "[staged]") {
		t.Fatalf("language toggle should stage a normalized diff:\n%s", model.View())
	}
	updated, _ = model.Update(cockpitRuneKey("l"))
	model = updated.(cockpitModel)
	view := model.View()
	if model.settings.dirty() {
		t.Fatalf("language toggled back to equivalent value should not be dirty")
	}
	if strings.Contains(view, "[staged]") {
		t.Fatalf("language row showed staged marker for equivalent normalized value:\n%s", view)
	}
	updated, _ = model.Update(cockpitRuneKey("w"))
	model = updated.(cockpitModel)
	if !strings.Contains(model.View(), "No pending settings changes to save.") {
		t.Fatalf("save prompt should match normalized clean state:\n%s", model.View())
	}
}

func TestCockpitSettingsSaveConfirmationHidesUnavailableGlobalShortcuts(t *testing.T) {
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.mode = cockpitModeSettings
	model.settings = cockpitSettingsState{
		loaded: true,
		snapshot: cockpitSettingsSnapshot{
			Path:   filepath.Join(t.TempDir(), "config.json"),
			Status: cockpitSettingsConfigMissing,
		},
	}
	model.settings.draft = model.settings.snapshot.Values.clone()

	updated, _ := model.Update(cockpitRuneKey("l"))
	model = updated.(cockpitModel)
	updated, _ = model.Update(cockpitRuneKey("w"))
	model = updated.(cockpitModel)
	view := model.View()
	if !strings.Contains(view, "y save · n/esc cancel") {
		t.Fatalf("save confirmation local help missing:\n%s", view)
	}
	for _, hidden := range []string{"1-6 sections", "tab/shift+tab", "? help", "esc back"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("save confirmation footer advertised unavailable global shortcut %q:\n%s", hidden, view)
		}
	}
}

func TestCockpitSettingsPatternInputKeepsQuitContract(t *testing.T) {
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.mode = cockpitModeSettings
	model.settings = cockpitSettingsState{
		loaded: true,
		snapshot: cockpitSettingsSnapshot{
			Path:   filepath.Join(t.TempDir(), "config.json"),
			Status: cockpitSettingsConfigMissing,
		},
	}
	model.settings.draft = model.settings.snapshot.Values.clone()

	updated, cmd := model.Update(cockpitRuneKey("n"))
	model = updated.(cockpitModel)
	if cmd != nil || !model.settings.editingPattern {
		t.Fatalf("n mode/cmd = editing:%v/%T, want editing/nil", model.settings.editingPattern, cmd)
	}
	view := model.View()
	if !strings.Contains(view, "ctrl+c quit") || strings.Contains(view, "q/ctrl+c quit") {
		t.Fatalf("pattern input footer should advertise ctrl+c quit only:\n%s", view)
	}
	for _, hidden := range []string{"1-6 sections", "tab/shift+tab", "? help", "esc back"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("pattern input footer advertised unavailable global shortcut %q:\n%s", hidden, view)
		}
	}
	updated, cmd = model.Update(cockpitRuneKey("q"))
	model = updated.(cockpitModel)
	if cmd != nil || model.settings.patternInput != "q" {
		t.Fatalf("q should remain available as regex input, pattern=%q cmd=%T", model.settings.patternInput, cmd)
	}
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatalf("ctrl+c in pattern input returned nil command, want quit")
	}
}

func TestCockpitModel_MemoryReviewSubmodeFootersDoNotAdvertiseBrowseActions(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-submode-footer", "submode footer candidate", domtypes.MemoryStatusCandidate)
	base := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	base.mode = cockpitModeMemoryReview
	base.memoryReview.items = []apptypes.MemoryDetails{candidate}
	base.memoryReview.review = newReviewModel(base.memoryReview.items, base.keys, base.styles)

	t.Run("edit", func(t *testing.T) {
		model := base
		model.memoryReview.review.mode = reviewModeEdit
		model.memoryReview.review.editIndex = 0
		view := model.View()
		for _, must := range []string{
			"enter commit · esc cancel · backspace edit",
			"ctrl+c quit",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("edit footer missing %q:\n%s", must, view)
			}
		}
		for _, mustNot := range []string{
			"a accept as-is · x reject",
			"esc back",
			"? help",
		} {
			if strings.Contains(view, mustNot) {
				t.Fatalf("edit footer advertised disabled %q:\n%s", mustNot, view)
			}
		}
	})

	t.Run("evidence", func(t *testing.T) {
		model := base
		model.memoryReview.review.mode = reviewModeViewEvidence
		view := model.View()
		for _, must := range []string{
			"v/esc close evidence · q finish/apply",
			"ctrl+c quit",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("evidence footer missing %q:\n%s", must, view)
			}
		}
		for _, mustNot := range []string{
			"a accept as-is · x reject",
			"1-6 sections",
		} {
			if strings.Contains(view, mustNot) {
				t.Fatalf("evidence footer advertised disabled %q:\n%s", mustNot, view)
			}
		}
	})

	t.Run("help", func(t *testing.T) {
		model := base
		model.showHelp = true
		model.memoryReview.review.mode = reviewModeHelp
		view := model.View()
		for _, must := range []string{
			"?/esc close help · q finish/apply",
			"Close memory review help",
		} {
			if !strings.Contains(view, must) {
				t.Fatalf("help footer/menu missing %q:\n%s", must, view)
			}
		}
		for _, mustNot := range []string{
			"Accept as-is only when the checklist passes",
			"Reject current candidate",
		} {
			if strings.Contains(view, mustNot) {
				t.Fatalf("help action menu advertised disabled %q:\n%s", mustNot, view)
			}
		}
	})
}

func TestCockpitModel_GlobalSectionKeysSwitchWithoutReturningToCLI(t *testing.T) {
	t.Parallel()

	candidate := cockpitMemoryDetailsFixture(t, "mem-global-nav", "review from global nav", domtypes.MemoryStatusCandidate)
	loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{candidate}}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(cockpitRuneKey("2"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeTop || cmd == nil {
		t.Fatalf("2 mode/cmd = %v/%T, want top/top load command", model.mode, cmd)
	}

	updated, cmd = model.Update(cockpitRuneKey("3"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeMemoryReview || cmd == nil {
		t.Fatalf("3 mode/cmd = %v/%T, want memory review/load command", model.mode, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("memory review load should mark memory seen")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("memory seen marker returned message = %T, want nil", msg)
	}
	updated, cmd = model.Update(cockpitRuneKey("a"))
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("accept key returned unexpected command = %T", cmd)
	}
	updated, cmd = model.Update(cockpitRuneKey("3"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeMemoryReview || cmd != nil {
		t.Fatalf("3 from memory review mode/cmd = %v/%T, want no-op memory review/nil", model.mode, cmd)
	}
	if got, want := len(model.memoryReview.review.Decisions()), 1; got != want {
		t.Fatalf("memory decisions after current-section key = %d, want %d", got, want)
	}
	if got, want := loader.reviewLoadCalls, 1; got != want {
		t.Fatalf("review load calls after current-section key = %d, want %d", got, want)
	}

	updated, cmd = model.Update(cockpitRuneKey("4"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeSessions || cmd != nil {
		t.Fatalf("4 from memory review mode/cmd = %v/%T, want sessions/nil", model.mode, cmd)
	}
	if len(loader.reviewFinishCalls) != 0 {
		t.Fatalf("global section switch applied memory decisions unexpectedly: %#v", loader.reviewFinishCalls)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeSettings || cmd != nil {
		t.Fatalf("tab from sessions mode/cmd = %v/%T, want settings/nil", model.mode, cmd)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeSessions || cmd != nil {
		t.Fatalf("shift+tab from settings mode/cmd = %v/%T, want sessions/nil", model.mode, cmd)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeSettings || cmd != nil {
		t.Fatalf("right from sessions mode/cmd = %v/%T, want settings/nil", model.mode, cmd)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("tab from settings mode/cmd = %v/%T, want tail/load", model.mode, cmd)
	}
}

func TestCockpitModel_EscBacksOutWithoutQuitting(t *testing.T) {
	t.Parallel()

	event := mustEvent(t, "evt-esc-detail", domtypes.EventKindNote, "detail esc event")
	loader := &cockpitLoaderStub{
		liveResponses: []cockpitLiveSnapshot{
			{Events: []*model.Event{event}, Cursor: newTailCursor(event.CreatedAt()), LoadedAt: fixedStartedAt},
		},
		detailContent: topDetailContent{title: "EVENT evt-esc-detail", lines: []string{"detail"}},
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.loader = loader
	model.loaderCtx = context.Background()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd != nil {
		t.Fatalf("esc from tail mode/cmd = %v/%T, want tail/nil", model.mode, cmd)
	}

	updated, cmd = model.Update(cockpitRuneKey("2"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeTop || cmd == nil {
		t.Fatalf("2 mode/cmd = %v/%T, want top/top load command", model.mode, cmd)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd != nil {
		t.Fatalf("esc from top mode/cmd = %v/%T, want tail/nil", model.mode, cmd)
	}

	model.live.events = append(model.live.events, event)
	model.live.selected = 0
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeDetail || cmd == nil {
		t.Fatalf("enter detail mode/cmd = %v/%T, want detail/load command", model.mode, cmd)
	}
	updated, _ = model.Update(cmd())
	model = updated.(cockpitModel)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeLive || cmd == nil {
		t.Fatalf("esc from detail mode/cmd = %v/%T, want live/tick command", model.mode, cmd)
	}

	_, cmd = model.Update(cockpitRuneKey("q"))
	if cmd == nil {
		t.Fatalf("q returned nil command, want quit command")
	}
}

type cockpitLoaderStub struct {
	homeResponses []cockpitHomeSnapshot
	homeCalls     int
	homeErr       error

	topResponses []topDataSnapshot
	topCalls     []topDataCriteria
	topErr       error
	topDetail    topDetailContent
	topDetailErr error
	topDetails   []topDetailRequest

	doctorResponses []cockpitDoctorSnapshot
	doctorCalls     int
	doctorErr       error

	memorySeenCalls []time.Time
	eventSeenCalls  []time.Time

	liveResponses []cockpitLiveSnapshot
	liveCalls     []cockpitLiveCall
	liveErr       error

	detailContent topDetailContent
	detailErr     error
	detailCalls   []domtypes.EventID

	reviewItems         []apptypes.MemoryDetails
	reviewLoadCalls     int
	reviewLoadStartedAt time.Time
	reviewLoadErr       error
	reviewFinishCalls   []reviewDecision
	reviewFinishResult  memoryInboxReviewResult
	reviewFinishErr     error
}

type cockpitLiveCall struct {
	cursor  tailCursor
	initial bool
}

func (s *cockpitLoaderStub) loadCockpitHome(context.Context) (cockpitHomeSnapshot, error) {
	s.homeCalls++
	if s.homeErr != nil {
		return cockpitHomeSnapshot{}, s.homeErr
	}
	if len(s.homeResponses) == 0 {
		return cockpitHomeSnapshot{LoadedAt: fixedStartedAt}, nil
	}
	next := s.homeResponses[0]
	s.homeResponses = s.homeResponses[1:]
	return next, nil
}

func (s *cockpitLoaderStub) loadCockpitTop(_ context.Context, criteria topDataCriteria) (topDataSnapshot, error) {
	s.topCalls = append(s.topCalls, criteria)
	if s.topErr != nil {
		return topDataSnapshot{}, s.topErr
	}
	if len(s.topResponses) == 0 {
		return topDataSnapshot{Now: fixedStartedAt}, nil
	}
	next := s.topResponses[0]
	s.topResponses = s.topResponses[1:]
	return next, nil
}

func (s *cockpitLoaderStub) loadCockpitTopDetail(_ context.Context, req topDetailRequest) (topDetailContent, error) {
	s.topDetails = append(s.topDetails, req)
	return s.topDetail, s.topDetailErr
}

func (s *cockpitLoaderStub) loadCockpitDoctor(context.Context) (cockpitDoctorSnapshot, error) {
	s.doctorCalls++
	if s.doctorErr != nil {
		return cockpitDoctorSnapshot{}, s.doctorErr
	}
	if len(s.doctorResponses) == 0 {
		return cockpitDoctorSnapshot{LoadedAt: fixedStartedAt}, nil
	}
	next := s.doctorResponses[0]
	s.doctorResponses = s.doctorResponses[1:]
	return next, nil
}

type cockpitStateReaderStub struct {
	at           time.Time
	ok           bool
	err          error
	eventAt      time.Time
	eventOk      bool
	eventSeenIDs []string
}

func (s cockpitStateReaderStub) MemoryLastSeenAt(context.Context) (time.Time, bool, error) {
	return s.at, s.ok, s.err
}

func (s cockpitStateReaderStub) EventLastSeenAt(context.Context) (time.Time, bool, error) {
	return s.eventAt, s.eventOk, s.err
}

func (s cockpitStateReaderStub) EventLastSeenIDs(context.Context) ([]string, bool, error) {
	return slices.Clone(s.eventSeenIDs), len(s.eventSeenIDs) > 0, s.err
}

func memorySummaryWithSourceAndUpdatedAt(t *testing.T, id string, status domtypes.MemoryStatus, source domtypes.MemorySource, updatedAt time.Time) apptypes.MemorySummary {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		"cockpit notification fixture "+id,
		status,
		domtypes.ConfidenceMedium,
		source,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedStartedAt,
		domtypes.None[time.Time](),
		fixedStartedAt,
		updatedAt,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func cockpitMemoryDetailsFixture(t *testing.T, id string, fact string, status domtypes.MemoryStatus) apptypes.MemoryDetails {
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
		domtypes.MemorySourceRememberIntent,
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
	evidence, err := domtypes.EvidenceRefFrom(domtypes.EvidenceRefKindFile, "/tmp/MEMORY.md#L1-L2")
	if err != nil {
		t.Fatalf("EvidenceRefFrom: %v", err)
	}
	return apptypes.MemoryDetailsOf(summary, []domtypes.EvidenceRef{evidence}, nil)
}

func cockpitEventFixtureAt(t *testing.T, id string, createdAt time.Time) *model.Event {
	t.Helper()
	event := mustEvent(t, id, domtypes.EventKindNote, "cockpit event notification fixture "+id)
	return model.EventOfWithSourceHook(event.EventID(), event.Kind(), event.Client(), event.Agent(), event.SessionID(), event.Workspace(), event.Body(), createdAt, event.SourceHook())
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

func (s *cockpitLoaderStub) loadCockpitMemoryReviewItems(context.Context) ([]apptypes.MemoryDetails, error) {
	s.reviewLoadStartedAt = time.Now()
	s.reviewLoadCalls++
	if s.reviewLoadErr != nil {
		return nil, s.reviewLoadErr
	}
	return s.reviewItems, nil
}

func (s *cockpitLoaderStub) finishCockpitMemoryReview(_ context.Context, final reviewModel, _ []apptypes.MemoryDetails) (memoryInboxReviewResult, error) {
	s.reviewFinishCalls = append(s.reviewFinishCalls, final.Decisions()...)
	return s.reviewFinishResult, s.reviewFinishErr
}

func (s *cockpitLoaderStub) markCockpitMemorySeen(_ context.Context, at time.Time) error {
	s.memorySeenCalls = append(s.memorySeenCalls, at)
	return nil
}

func (s *cockpitLoaderStub) markCockpitEventsSeen(_ context.Context, at time.Time, _ []string) error {
	s.eventSeenCalls = append(s.eventSeenCalls, at)
	return nil
}

func cockpitRuneKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
