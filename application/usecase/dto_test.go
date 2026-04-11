package usecase_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewEventDetails(t *testing.T) {
	t.Parallel()

	eventID, _ := types.EventIDOf("evt-1")
	kind, _ := types.EventKindOf("note")
	agent, _ := types.AgentOf("claude")
	sid, _ := types.SessionIDOf("session-1")

	event, err := model.NewEvent(eventID, kind, "cli", agent, sid, "workspace", "test body")
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}

	t.Run("with event only", func(t *testing.T) {
		t.Parallel()
		details, err := usecase.NewEventDetails(event, nil)
		if err != nil {
			t.Fatalf("NewEventDetails() error = %v", err)
		}
		if details.Event() != event {
			t.Errorf("Event() mismatch")
		}
		if details.CommandAudit() != nil {
			t.Errorf("CommandAudit() = %v, want nil", details.CommandAudit())
		}
	})

	t.Run("with event and command audit", func(t *testing.T) {
		t.Parallel()
		audit, err := model.NewCommandAudit(eventID, "ls -la", "", "", false, false)
		if err != nil {
			t.Fatalf("NewCommandAudit() error = %v", err)
		}
		details, err := usecase.NewEventDetails(event, audit)
		if err != nil {
			t.Fatalf("NewEventDetails() error = %v", err)
		}
		if details.CommandAudit() != audit {
			t.Errorf("CommandAudit() mismatch")
		}
	})

	t.Run("nil event returns error", func(t *testing.T) {
		t.Parallel()
		_, err := usecase.NewEventDetails(nil, nil)
		if err == nil {
			t.Fatal("NewEventDetails(nil, nil) should return error")
		}
	})
}

func TestSessionSummary_fields(t *testing.T) {
	t.Parallel()

	sid, _ := types.SessionIDOf("session-1")
	ws, _ := types.WorkspaceOf("github.com/duck8823/traceary")
	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)

	summary := usecase.SessionSummary{
		SessionID:   sid,
		Workspace:   ws,
		StartedAt:   now,
		Status:      "active",
		TotalEvents: 5,
		Label:       "sprint-1",
	}

	if summary.SessionID != sid {
		t.Errorf("SessionID = %v, want %v", summary.SessionID, sid)
	}
	if summary.Workspace != ws {
		t.Errorf("Workspace = %v, want %v", summary.Workspace, ws)
	}
	if summary.Status != "active" {
		t.Errorf("Status = %q, want %q", summary.Status, "active")
	}
}

func TestHandoffSummary_fields(t *testing.T) {
	t.Parallel()

	sid, _ := types.SessionIDOf("session-1")
	ws, _ := types.WorkspaceOf("github.com/duck8823/traceary")

	handoff := usecase.HandoffSummary{
		SessionID:      sid,
		Workspace:      ws,
		Label:          "sprint-1",
		Status:         "active",
		RecentCommands: []string{"git status", "go test ./..."},
	}

	if handoff.SessionID != sid {
		t.Errorf("SessionID = %v, want %v", handoff.SessionID, sid)
	}
	if len(handoff.RecentCommands) != 2 {
		t.Errorf("RecentCommands length = %d, want 2", len(handoff.RecentCommands))
	}
}
