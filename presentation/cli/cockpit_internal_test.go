package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
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

	view := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home).View()
	for _, must := range []string{
		"new candidate memories=2",
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
	view := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home).View()
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
	view := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home).View()
	if !strings.Contains(view, "candidate memories=1") {
		t.Fatalf("fallback view should still surface total candidates:\n%s", view)
	}
	if strings.Contains(view, "new candidate memories=") {
		t.Fatalf("fallback view should not claim a new count without last-seen state:\n%s", view)
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
	view := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home).View()
	if !strings.Contains(view, "memories: accepted(reviewed)=2 candidate(inbox)=0 new=0 remember-intent=0 low-quality=0 stale=0") {
		t.Fatalf("none-state view missing zero notification summary:\n%s", view)
	}
	if strings.Contains(view, "candidate memories=") || strings.Contains(view, "new candidate memories=") {
		t.Fatalf("none-state view should not show candidate warnings:\n%s", view)
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
		"memories: accepted(reviewed)=2 candidate(inbox)=0 new=untracked remember-intent=0 low-quality=0 stale=0",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("cockpit view missing %q:\n%s", must, view)
		}
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
	if model.mode != cockpitModeHome || cmd != nil {
		t.Fatalf("doctor h mode/cmd = %v/%T, want home/nil", model.mode, cmd)
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
	if cmd == nil {
		t.Fatalf("initial load should mark events seen")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("event seen marker returned message = %T, want nil", msg)
	}
	if got, want := len(loader.eventSeenCalls), 1; got != want {
		t.Fatalf("event seen calls after initial load = %d, want %d", got, want)
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

	updated, cmd := model.Update(cockpitRuneKey("t"))
	model = updated.(cockpitModel)
	if cmd == nil {
		t.Fatalf("opening live pane returned nil command")
	}
	updated, homeCmd := model.Update(cockpitRuneKey("h"))
	model = updated.(cockpitModel)
	if model.mode != cockpitModeHome || homeCmd != nil {
		t.Fatalf("leaving live mode/cmd = %v/%T, want home/nil", model.mode, homeCmd)
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
	if model.mode != cockpitModeHome {
		t.Fatalf("after apply mode = %v, want home", model.mode)
	}
	if cmd == nil {
		t.Fatalf("after apply should refresh cockpit home")
	}
	if got, want := len(loader.reviewFinishCalls), 1; got != want {
		t.Fatalf("review finish decision count = %d, want %d", got, want)
	}
	if got := loader.reviewFinishCalls[0].kind; got != reviewDecisionAccept {
		t.Fatalf("review finish decision = %v, want accept", got)
	}

	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("home refresh returned unexpected command = %T", cmd)
	}
	if got, want := loader.homeCalls, 1; got != want {
		t.Fatalf("home refresh calls = %d, want %d", got, want)
	}
	if got, want := model.home.CandidateMemoryCount, 0; got != want {
		t.Fatalf("refreshed CandidateMemoryCount = %d, want %d", got, want)
	}
	if got := model.View(); !strings.Contains(got, "memory review applied: accepted=1 rejected=0 distilled=0 failures=0") || !strings.Contains(got, "candidate(inbox)=0") {
		t.Fatalf("home view missing review result/refreshed counts:\n%s", got)
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
	if model.mode != cockpitModeHome {
		t.Fatalf("esc from review browse mode = %v, want home", model.mode)
	}
	if model.memoryReview.applying {
		t.Fatalf("esc from review browse started applying decisions")
	}
	if len(loader.reviewFinishCalls) != 0 {
		t.Fatalf("esc from review browse applied decisions unexpectedly: %#v", loader.reviewFinishCalls)
	}
	if cmd == nil {
		t.Fatalf("esc from review browse returned nil command, want home refresh")
	}

	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("home refresh returned unexpected command = %T", cmd)
	}
	if got, want := loader.homeCalls, 1; got != want {
		t.Fatalf("home refresh calls = %d, want %d", got, want)
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
	if model.mode != cockpitModeHome || cmd == nil {
		t.Fatalf("h from review error mode/cmd = %v/%T, want home/refresh command", model.mode, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("home refresh after h returned follow-up command = %T", cmd)
	}
	if got, want := model.home.CandidateMemoryCount, 3; got != want {
		t.Fatalf("home refresh after h CandidateMemoryCount = %d, want %d", got, want)
	}

	model.mode = cockpitModeMemoryReview
	model.memoryReview.err = context.Canceled
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeHome || cmd == nil {
		t.Fatalf("esc from review error mode/cmd = %v/%T, want home/refresh command", model.mode, cmd)
	}
	updated, cmd = model.Update(cmd())
	model = updated.(cockpitModel)
	if cmd != nil {
		t.Fatalf("home refresh after esc returned follow-up command = %T", cmd)
	}
	if got, want := model.home.CandidateMemoryCount, 4; got != want {
		t.Fatalf("home refresh after esc CandidateMemoryCount = %d, want %d", got, want)
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

	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: fixedStartedAt})
	model.mode = cockpitModeMemoryReview
	model.memoryReview.loading = true
	model.memoryReview.requestSeq = 1

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(cockpitModel)
	if model.mode != cockpitModeHome || cmd != nil {
		t.Fatalf("esc while review loading mode/cmd = %v/%T, want home/nil", model.mode, cmd)
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
		reviewFinishErr:    memoryInboxReviewFailureError(result),
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
	if model.mode != cockpitModeHome {
		t.Fatalf("h while review loading mode = %v, want home", model.mode)
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

type cockpitLoaderStub struct {
	homeResponses []cockpitHomeSnapshot
	homeCalls     int
	homeErr       error

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
