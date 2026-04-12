package model_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewSession(t *testing.T) {
	t.Parallel()

	agent, err := types.AgentOf("claude")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sid, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	session := model.NewSession(sid, now, "hook", agent, "duck8823/traceary")

	if diff := cmp.Diff(sid, session.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(now, session.StartedAt()); diff != "" {
		t.Errorf("StartedAt() mismatch (-want +got):\n%s", diff)
	}
	if endedAt, ok := session.EndedAt().Get(); ok {
		t.Errorf("EndedAt() should be empty, got %v", endedAt)
	}
	if diff := cmp.Diff("hook", session.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(agent, session.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("duck8823/traceary", session.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", session.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", session.Summary()); diff != "" {
		t.Errorf("Summary() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", session.ParentSessionID()); diff != "" {
		t.Errorf("ParentSessionID() mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionOf(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentOf("codex")
	sid, _ := types.SessionIDOf("session-2")
	start := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 10, 13, 0, 0, 0, time.UTC)

	session := model.SessionOf(sid, start, types.Of(end), "cli", agent, "workspace", "sprint-1", "did stuff", "parent-123")

	if diff := cmp.Diff(sid, session.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if endedAt, ok := session.EndedAt().Get(); !ok {
		t.Errorf("EndedAt() should be present")
	} else if diff := cmp.Diff(end, endedAt); diff != "" {
		t.Errorf("EndedAt() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("sprint-1", session.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("did stuff", session.Summary()); diff != "" {
		t.Errorf("Summary() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("parent-123", session.ParentSessionID()); diff != "" {
		t.Errorf("ParentSessionID() mismatch (-want +got):\n%s", diff)
	}
}

func TestSession_SetLabel(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentOf("claude")
	sid, _ := types.SessionIDOf("session-3")
	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)

	session := model.NewSession(sid, now, "cli", agent, "duck8823/traceary")

	if session.Label() != "" {
		t.Fatalf("Label() = %q, want empty before SetLabel", session.Label())
	}

	session.SetLabel("sprint-1")
	if diff := cmp.Diff("sprint-1", session.Label()); diff != "" {
		t.Errorf("Label() after SetLabel mismatch (-want +got):\n%s", diff)
	}

	session.SetLabel("updated-label")
	if diff := cmp.Diff("updated-label", session.Label()); diff != "" {
		t.Errorf("Label() after second SetLabel mismatch (-want +got):\n%s", diff)
	}

	session.SetLabel("")
	if diff := cmp.Diff("", session.Label()); diff != "" {
		t.Errorf("Label() after clearing with SetLabel mismatch (-want +got):\n%s", diff)
	}
}
