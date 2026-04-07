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
			name:        "前後空白を除去してイベントを生成できる",
			body:        "  hello traceary  ",
			wantBody:    "hello traceary",
			wantCreated: fixedTime,
			wantErr:     false,
		},
		{
			name:    "空白のみの本文はエラー",
			body:    "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.NewEvent(
				eventID,
				types.EventKindNote,
				agent,
				sessionID,
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
			if got.CreatedAt() != tt.wantCreated {
				t.Fatalf("CreatedAt() = %v, want %v", got.CreatedAt(), tt.wantCreated)
			}
		})
	}
}
