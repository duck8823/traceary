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
	workspaces := []string{"github.com/org/repo-a", "github.com/org/repo-b"}
	agents := []string{"claude", "codex"}
	kinds := []string{"note", "command_executed"}

	block := apptypes.TimelineBlockOf(blockStart, blockEnd, 5, workspaces, agents, kinds)

	if !block.BlockStart().Equal(blockStart) {
		t.Errorf("BlockStart() = %v, want %v", block.BlockStart(), blockStart)
	}
	if !block.BlockEnd().Equal(blockEnd) {
		t.Errorf("BlockEnd() = %v, want %v", block.BlockEnd(), blockEnd)
	}
	if diff := cmp.Diff(5, block.EventCount()); diff != "" {
		t.Errorf("EventCount() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(workspaces, block.Workspaces()); diff != "" {
		t.Errorf("Workspaces() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(agents, block.Agents()); diff != "" {
		t.Errorf("Agents() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(kinds, block.Kinds()); diff != "" {
		t.Errorf("Kinds() mismatch (-want +got):\n%s", diff)
	}
}

func TestTimelineBlock_DefensiveCopy(t *testing.T) {
	t.Parallel()

	workspaces := []string{"ws-a", "ws-b"}
	agents := []string{"claude", "codex"}
	kinds := []string{"note", "command_executed"}

	block := apptypes.TimelineBlockOf(
		time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
		time.Date(2024, time.January, 2, 4, 4, 5, 0, time.UTC),
		3,
		workspaces,
		agents,
		kinds,
	)

	// Mutate source slices after construction.
	workspaces[0] = "mutated-source-ws"
	agents[0] = "mutated-source-agent"
	kinds[0] = "mutated-source-kind"

	if diff := cmp.Diff([]string{"ws-a", "ws-b"}, block.Workspaces()); diff != "" {
		t.Errorf("Workspaces source slice leaked (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"claude", "codex"}, block.Agents()); diff != "" {
		t.Errorf("Agents source slice leaked (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"note", "command_executed"}, block.Kinds()); diff != "" {
		t.Errorf("Kinds source slice leaked (-want +got):\n%s", diff)
	}

	// Mutate returned slices from getters.
	retWorkspaces := block.Workspaces()
	retWorkspaces[0] = "mutated-return-ws"
	retAgents := block.Agents()
	retAgents[0] = "mutated-return-agent"
	retKinds := block.Kinds()
	retKinds[0] = "mutated-return-kind"

	if diff := cmp.Diff([]string{"ws-a", "ws-b"}, block.Workspaces()); diff != "" {
		t.Errorf("Workspaces() is not a defensive copy (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"claude", "codex"}, block.Agents()); diff != "" {
		t.Errorf("Agents() is not a defensive copy (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"note", "command_executed"}, block.Kinds()); diff != "" {
		t.Errorf("Kinds() is not a defensive copy (-want +got):\n%s", diff)
	}
}
