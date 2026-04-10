package model_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewEvent(t *testing.T) {
	fixedTime := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	model.SetNowFunc(func() time.Time { return fixedTime })
	defer model.ResetNowFunc()

	eventID, err := types.EventIDOf("event-1")
	if err != nil {
		t.Fatalf("EventIDOf() error = %v", err)
	}
	agent, err := types.AgentOf("codex")
	if err != nil {
		t.Fatalf("AgentOf() error = %v", err)
	}
	sessionID, err := types.SessionIDOf("session-1")
	if err != nil {
		t.Fatalf("SessionIDOf() error = %v", err)
	}

	tests := []struct {
		name        string
		body        string
		wantBody    string
		wantCreated time.Time
		wantErr     bool
	}{
		{
			name:        "trims whitespace and creates event",
			body:        "  hello traceary  ",
			wantBody:    "hello traceary",
			wantCreated: fixedTime,
			wantErr:     false,
		},
		{
			name:    "returns error for whitespace-only body",
			body:    "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.NewEvent(
				eventID,
				types.EventKindNote,
				"cli",
				agent,
				sessionID,
				"duck8823/traceary",
				tt.body,
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Body() != tt.wantBody {
				t.Fatalf("Body() = %q, want %q", got.Body(), tt.wantBody)
			}
			if got.Client() != "cli" {
				t.Fatalf("Client() = %q, want %q", got.Client(), "cli")
			}
			if got.Repo() != "duck8823/traceary" {
				t.Fatalf("Repo() = %q, want %q", got.Repo(), "duck8823/traceary")
			}
			if got.CreatedAt() != tt.wantCreated {
				t.Fatalf("CreatedAt() = %v, want %v", got.CreatedAt(), tt.wantCreated)
			}
		})
	}
}

func TestEventOf(t *testing.T) {
	t.Parallel()

	eventID, _ := types.EventIDOf("event-1")
	agent, _ := types.AgentOf("claude")
	sessionID, _ := types.SessionIDOf("session-1")
	ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	event := model.EventOf(eventID, types.EventKindNote, "cli", agent, sessionID, "duck8823/traceary", "hello", ts)

	if event.EventID() != eventID {
		t.Errorf("EventID() = %v, want %v", event.EventID(), eventID)
	}
	if event.Kind() != types.EventKindNote {
		t.Errorf("Kind() = %v, want %v", event.Kind(), types.EventKindNote)
	}
	if event.Agent() != agent {
		t.Errorf("Agent() = %v, want %v", event.Agent(), agent)
	}
	if event.SessionID() != sessionID {
		t.Errorf("SessionID() = %v, want %v", event.SessionID(), sessionID)
	}
	if event.CreatedAt() != ts {
		t.Errorf("CreatedAt() = %v, want %v", event.CreatedAt(), ts)
	}
}
