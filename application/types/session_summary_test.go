package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestSessionSummaryOf_Getters(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	endedAt := domtypes.Some(startedAt.Add(1 * time.Hour))
	agents := []string{"claude", "codex"}

	summary := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/org/repo"),
		startedAt,
		endedAt,
		"active",
		42,
		7,
		agents,
		"daily-standup",
		"body text",
		domtypes.SessionID("parent-session"),
		domtypes.EventID("spawn-event"),
		"task",
		domtypes.Some(4),
		startedAt.Add(30*time.Minute),
		apptypes.SessionSummaryLatestEventOf(domtypes.EventKindTranscript, "assistant reply"),
	)

	if diff := cmp.Diff(domtypes.SessionID("session-1"), summary.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("github.com/org/repo"), summary.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if !summary.StartedAt().Equal(startedAt) {
		t.Errorf("StartedAt() = %v, want %v", summary.StartedAt(), startedAt)
	}
	gotEndedAt, ok := summary.EndedAt().Value()
	if !ok {
		t.Fatalf("EndedAt().Value() ok = false, want true")
	}
	if !gotEndedAt.Equal(startedAt.Add(1 * time.Hour)) {
		t.Errorf("EndedAt() = %v, want %v", gotEndedAt, startedAt.Add(1*time.Hour))
	}
	if diff := cmp.Diff("active", summary.Status()); diff != "" {
		t.Errorf("Status() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(42, summary.TotalEvents()); diff != "" {
		t.Errorf("TotalEvents() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(7, summary.CommandCount()); diff != "" {
		t.Errorf("CommandCount() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"claude", "codex"}, summary.Agents()); diff != "" {
		t.Errorf("Agents() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("daily-standup", summary.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("body text", summary.Summary()); diff != "" {
		t.Errorf("Summary() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID("parent-session"), summary.ParentSessionID()); diff != "" {
		t.Errorf("ParentSessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.EventID("spawn-event"), summary.SpawnEventID()); diff != "" {
		t.Errorf("SpawnEventID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("task", summary.SubagentKind()); diff != "" {
		t.Errorf("SubagentKind() mismatch (-want +got):\n%s", diff)
	}
	if spawnOrder, ok := summary.SpawnOrder().Value(); !ok {
		t.Fatalf("SpawnOrder() should be present")
	} else if diff := cmp.Diff(4, spawnOrder); diff != "" {
		t.Errorf("SpawnOrder() mismatch (-want +got):\n%s", diff)
	}
	if !summary.LatestEventAt().Equal(startedAt.Add(30 * time.Minute)) {
		t.Errorf("LatestEventAt() = %v, want %v", summary.LatestEventAt(), startedAt.Add(30*time.Minute))
	}
	if diff := cmp.Diff(domtypes.EventKindTranscript, summary.LatestEventKind()); diff != "" {
		t.Errorf("LatestEventKind() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("assistant reply", summary.LatestEventMessage()); diff != "" {
		t.Errorf("LatestEventMessage() mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionSummaryOf_EmptyEndedAt(t *testing.T) {
	t.Parallel()

	summary := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("ws"),
		time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
		domtypes.None[time.Time](),
		"active",
		0,
		0,
		nil,
		"",
		"",
		domtypes.SessionID(""),
	)

	if _, ok := summary.EndedAt().Value(); ok {
		t.Errorf("EndedAt().Value() = true, want false")
	}
}

func TestSessionSummary_AgentsDefensiveCopy(t *testing.T) {
	t.Parallel()

	original := []string{"claude", "codex"}
	summary := apptypes.SessionSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("ws"),
		time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
		domtypes.None[time.Time](),
		"active",
		0,
		0,
		original,
		"",
		"",
		domtypes.SessionID(""),
	)

	// Mutating the source slice after construction must not affect the stored state.
	original[0] = "mutated-source"
	if diff := cmp.Diff([]string{"claude", "codex"}, summary.Agents()); diff != "" {
		t.Errorf("constructor did not defensively copy source slice (-want +got):\n%s", diff)
	}

	// Mutating the returned slice must not affect the stored state.
	returned := summary.Agents()
	returned[0] = "mutated-return"
	if diff := cmp.Diff([]string{"claude", "codex"}, summary.Agents()); diff != "" {
		t.Errorf("returned slice is not a defensive copy (-want +got):\n%s", diff)
	}
}
