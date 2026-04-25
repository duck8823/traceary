package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewSession(t *testing.T) {
	t.Parallel()

	agent, err := types.AgentFrom("claude")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sid, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	session := model.NewSession(sid, now, types.Client("hook"), agent, types.Workspace("duck8823/traceary"))

	if diff := cmp.Diff(sid, session.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(now, session.StartedAt()); diff != "" {
		t.Errorf("StartedAt() mismatch (-want +got):\n%s", diff)
	}
	if endedAt, ok := session.EndedAt().Value(); ok {
		t.Errorf("EndedAt() should be empty, got %v", endedAt)
	}
	if diff := cmp.Diff(types.Client("hook"), session.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(agent, session.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.Workspace("duck8823/traceary"), session.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", session.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", session.Summary()); diff != "" {
		t.Errorf("Summary() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(types.SessionID(""), session.ParentSessionID()); diff != "" {
		t.Errorf("ParentSessionID() mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionOf(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentFrom("codex")
	sid, _ := types.SessionIDFrom("session-2")
	start := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 10, 13, 0, 0, 0, time.UTC)

	session := model.SessionOf(sid, start, types.Some(end), types.Client("cli"), agent, types.Workspace("workspace"), "sprint-1", "did stuff", types.SessionID("parent-123"))

	if diff := cmp.Diff(sid, session.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if endedAt, ok := session.EndedAt().Value(); !ok {
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
	if diff := cmp.Diff(types.SessionID("parent-123"), session.ParentSessionID()); diff != "" {
		t.Errorf("ParentSessionID() mismatch (-want +got):\n%s", diff)
	}
}

func TestSession_End(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentFrom("claude")
	sid, _ := types.SessionIDFrom("session-end")
	start := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 11, 13, 30, 0, 0, time.UTC)

	t.Run("ends an active session", func(t *testing.T) {
		t.Parallel()

		session := model.NewSession(sid, start, types.Client("cli"), agent, types.Workspace("duck8823/traceary"))
		session.SetLabel("sprint-1")

		if _, ok := session.EndedAt().Value(); ok {
			t.Fatalf("EndedAt() should be empty before End()")
		}

		if err := session.End(end, "wrapped up"); err != nil {
			t.Fatalf("End() error = %v", err)
		}

		endedAt, ok := session.EndedAt().Value()
		if !ok {
			t.Fatalf("EndedAt() should be present after End()")
		}
		if diff := cmp.Diff(end, endedAt); diff != "" {
			t.Errorf("EndedAt() mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("wrapped up", session.Summary()); diff != "" {
			t.Errorf("Summary() mismatch (-want +got):\n%s", diff)
		}
		// End() must not touch the label.
		if diff := cmp.Diff("sprint-1", session.Label()); diff != "" {
			t.Errorf("Label() should be unchanged by End(), mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns ErrInvalidSessionState when ending an already-ended session", func(t *testing.T) {
		t.Parallel()

		alreadyEnded := model.SessionOf(
			sid, start, types.Some(end),
			types.Client("cli"), agent, types.Workspace("duck8823/traceary"),
			"", "first end", types.SessionID(""),
		)

		err := alreadyEnded.End(end.Add(time.Hour), "second end attempt")
		if err == nil {
			t.Fatalf("End() error = nil, want ErrInvalidSessionState")
		}
		if !errors.Is(err, model.ErrInvalidSessionState) {
			t.Fatalf("End() error = %v, want ErrInvalidSessionState", err)
		}
		// Original ended_at should be preserved.
		got, _ := alreadyEnded.EndedAt().Value()
		if diff := cmp.Diff(end, got); diff != "" {
			t.Errorf("EndedAt() should be unchanged (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("first end", alreadyEnded.Summary()); diff != "" {
			t.Errorf("Summary() should be unchanged (-want +got):\n%s", diff)
		}
	})
}

func TestSession_SetLabel(t *testing.T) {
	t.Parallel()

	agent, _ := types.AgentFrom("claude")
	sid, _ := types.SessionIDFrom("session-3")
	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)

	session := model.NewSession(sid, now, types.Client("cli"), agent, types.Workspace("duck8823/traceary"))

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
