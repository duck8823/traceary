package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestHandoffSummaryOf_Getters(t *testing.T) {
	t.Parallel()

	agents := []string{"claude", "codex"}
	recentCommands := []string{"go test ./...", "git status"}

	handoff := apptypes.HandoffSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/org/repo"),
		"feature/foo",
		"active",
		10,
		3,
		agents,
		"session summary text",
		recentCommands,
	)

	if diff := cmp.Diff(domtypes.SessionID("session-1"), handoff.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("github.com/org/repo"), handoff.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("feature/foo", handoff.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("active", handoff.Status()); diff != "" {
		t.Errorf("Status() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(10, handoff.TotalEvents()); diff != "" {
		t.Errorf("TotalEvents() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(3, handoff.CommandCount()); diff != "" {
		t.Errorf("CommandCount() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(agents, handoff.Agents()); diff != "" {
		t.Errorf("Agents() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("session summary text", handoff.Summary()); diff != "" {
		t.Errorf("Summary() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(recentCommands, handoff.RecentCommands()); diff != "" {
		t.Errorf("RecentCommands() mismatch (-want +got):\n%s", diff)
	}
}

func TestHandoffSummary_DefensiveCopy(t *testing.T) {
	t.Parallel()

	agents := []string{"claude", "codex"}
	recentCommands := []string{"go test ./...", "git status"}

	handoff := apptypes.HandoffSummaryOf(
		domtypes.SessionID("session-1"),
		domtypes.Workspace("ws"),
		"label",
		"active",
		0,
		0,
		agents,
		"summary",
		recentCommands,
	)

	// Mutating source slices after construction must not affect internal state.
	agents[0] = "mutated-source-agent"
	recentCommands[0] = "mutated-source-command"

	if diff := cmp.Diff([]string{"claude", "codex"}, handoff.Agents()); diff != "" {
		t.Errorf("Agents source slice leaked (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"go test ./...", "git status"}, handoff.RecentCommands()); diff != "" {
		t.Errorf("RecentCommands source slice leaked (-want +got):\n%s", diff)
	}

	// Mutating returned slices from getters.
	retAgents := handoff.Agents()
	retAgents[0] = "mutated-return-agent"
	retCommands := handoff.RecentCommands()
	retCommands[0] = "mutated-return-command"

	if diff := cmp.Diff([]string{"claude", "codex"}, handoff.Agents()); diff != "" {
		t.Errorf("Agents() is not a defensive copy (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"go test ./...", "git status"}, handoff.RecentCommands()); diff != "" {
		t.Errorf("RecentCommands() is not a defensive copy (-want +got):\n%s", diff)
	}
}
