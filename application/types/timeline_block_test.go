package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestTimelineBlockOf_Getters(t *testing.T) {
	t.Parallel()

	blockStart := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	blockEnd := blockStart.Add(30 * time.Minute)
	agents := []string{"claude", "codex"}
	breakdown := []apptypes.TimelineWorkspaceBreakdown{
		apptypes.TimelineWorkspaceBreakdownOf(
			"github.com/org/repo-a",
			3,
			[]string{"note", "command_executed"},
			[]string{"claude"},
			"fix the thing",
			apptypes.TimelineSummarySourcePrompt,
		),
		apptypes.TimelineWorkspaceBreakdownOf(
			"github.com/org/repo-b",
			2,
			[]string{"command_executed"},
			[]string{"codex"},
			"",
			apptypes.TimelineSummarySourceKindCounts,
		),
	}

	block := apptypes.TimelineBlockOf(blockStart, blockEnd, 5, agents, breakdown)

	if !block.BlockStart().Equal(blockStart) {
		t.Errorf("BlockStart() = %v, want %v", block.BlockStart(), blockStart)
	}
	if !block.BlockEnd().Equal(blockEnd) {
		t.Errorf("BlockEnd() = %v, want %v", block.BlockEnd(), blockEnd)
	}
	if diff := cmp.Diff(5, block.EventCount()); diff != "" {
		t.Errorf("EventCount() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"github.com/org/repo-a", "github.com/org/repo-b"}, block.Workspaces()); diff != "" {
		t.Errorf("Workspaces() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(agents, block.Agents()); diff != "" {
		t.Errorf("Agents() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"note", "command_executed", "command_executed"}, block.Kinds()); diff != "" {
		t.Errorf("Kinds() mismatch (-want +got):\n%s", diff)
	}

	returnedBreakdown := block.WorkspaceBreakdown()
	if len(returnedBreakdown) != 2 {
		t.Fatalf("WorkspaceBreakdown() len = %d, want 2", len(returnedBreakdown))
	}
	if got := returnedBreakdown[0].Summary(); got != "fix the thing" {
		t.Errorf("breakdown[0].Summary() = %q, want %q", got, "fix the thing")
	}
	if got := returnedBreakdown[0].SummarySource(); got != apptypes.TimelineSummarySourcePrompt {
		t.Errorf("breakdown[0].SummarySource() = %q, want %q", got, apptypes.TimelineSummarySourcePrompt)
	}
	if got := returnedBreakdown[1].SummarySource(); got != apptypes.TimelineSummarySourceKindCounts {
		t.Errorf("breakdown[1].SummarySource() = %q, want %q", got, apptypes.TimelineSummarySourceKindCounts)
	}
}

func TestTimelineBlock_DefensiveCopy(t *testing.T) {
	t.Parallel()

	agents := []string{"claude", "codex"}
	breakdown := []apptypes.TimelineWorkspaceBreakdown{
		apptypes.TimelineWorkspaceBreakdownOf(
			"ws-a",
			2,
			[]string{"note"},
			nil,
			"",
			apptypes.TimelineSummarySourceKindCounts,
		),
	}

	block := apptypes.TimelineBlockOf(
		time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
		time.Date(2024, time.January, 2, 4, 4, 5, 0, time.UTC),
		2,
		agents,
		breakdown,
	)

	// Mutate source slices after construction.
	agents[0] = "mutated-source-agent"
	breakdown[0] = apptypes.TimelineWorkspaceBreakdownOf("mutated", 99, nil, nil, "", apptypes.TimelineSummarySourceKindCounts)

	if diff := cmp.Diff([]string{"claude", "codex"}, block.Agents()); diff != "" {
		t.Errorf("Agents source slice leaked (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ws-a"}, block.Workspaces()); diff != "" {
		t.Errorf("Workspaces derived slice leaked (-want +got):\n%s", diff)
	}

	// Mutate returned slices from getters.
	retAgents := block.Agents()
	retAgents[0] = "mutated-return-agent"
	retBreakdown := block.WorkspaceBreakdown()
	retBreakdown[0] = apptypes.TimelineWorkspaceBreakdownOf("mutated", 99, nil, nil, "", apptypes.TimelineSummarySourceKindCounts)

	if diff := cmp.Diff([]string{"claude", "codex"}, block.Agents()); diff != "" {
		t.Errorf("Agents() is not a defensive copy (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ws-a"}, block.Workspaces()); diff != "" {
		t.Errorf("WorkspaceBreakdown() is not a defensive copy (-want +got):\n%s", diff)
	}
}
