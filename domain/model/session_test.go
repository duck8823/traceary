package model_test

import (
	"testing"
	"time"

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

	if session.SessionID() != sid {
		t.Errorf("SessionID() = %v, want %v", session.SessionID(), sid)
	}
	if session.StartedAt() != now {
		t.Errorf("StartedAt() = %v, want %v", session.StartedAt(), now)
	}
	if session.EndedAt() != nil {
		t.Errorf("EndedAt() = %v, want nil", session.EndedAt())
	}
	if session.Client() != "hook" {
		t.Errorf("Client() = %q, want %q", session.Client(), "hook")
	}
	if session.Agent() != agent {
		t.Errorf("Agent() = %v, want %v", session.Agent(), agent)
	}
	if session.Repo() != "duck8823/traceary" {
		t.Errorf("Repo() = %q, want %q", session.Repo(), "duck8823/traceary")
	}
	if session.Label() != "" {
		t.Errorf("Label() = %q, want empty", session.Label())
	}
	if session.Summary() != "" {
		t.Errorf("Summary() = %q, want empty", session.Summary())
	}
	if session.ParentSessionID() != "" {
		t.Errorf("ParentSessionID() = %q, want empty", session.ParentSessionID())
	}
}

func TestSessionOf(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentOf("codex")
	sid, _ := types.SessionIDOf("session-2")
	start := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 10, 13, 0, 0, 0, time.UTC)

	session := model.SessionOf(sid, start, &end, "cli", agent, "repo", "sprint-1", "did stuff", "parent-123")

	if session.SessionID() != sid {
		t.Errorf("SessionID() = %v, want %v", session.SessionID(), sid)
	}
	if session.EndedAt() == nil || *session.EndedAt() != end {
		t.Errorf("EndedAt() = %v, want %v", session.EndedAt(), end)
	}
	if session.Label() != "sprint-1" {
		t.Errorf("Label() = %q, want %q", session.Label(), "sprint-1")
	}
	if session.Summary() != "did stuff" {
		t.Errorf("Summary() = %q, want %q", session.Summary(), "did stuff")
	}
	if session.ParentSessionID() != "parent-123" {
		t.Errorf("ParentSessionID() = %q, want %q", session.ParentSessionID(), "parent-123")
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
	if session.Label() != "sprint-1" {
		t.Errorf("Label() = %q, want %q after SetLabel", session.Label(), "sprint-1")
	}

	session.SetLabel("updated-label")
	if session.Label() != "updated-label" {
		t.Errorf("Label() = %q, want %q after second SetLabel", session.Label(), "updated-label")
	}
}
