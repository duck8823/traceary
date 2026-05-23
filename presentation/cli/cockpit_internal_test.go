package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
