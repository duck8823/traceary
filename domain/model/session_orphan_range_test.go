package model_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewSessionOrphanRange_EnforcesInvariants(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	valid, err := model.NewSessionOrphanRange(
		"sess-1",
		types.Some(types.EventID("evt-from")),
		"evt-to",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("NewSessionOrphanRange() error = %v", err)
	}
	if valid.SessionID() != "sess-1" || valid.ToEventID() != "evt-to" {
		t.Fatalf("got session=%s to=%s", valid.SessionID(), valid.ToEventID())
	}
	if from, ok := valid.FromEventID().Value(); !ok || from != "evt-from" {
		t.Fatalf("FromEventID = %v present=%v", from, ok)
	}

	tests := []struct {
		name      string
		sessionID types.SessionID
		from      types.Optional[types.EventID]
		to        types.EventID
		at        time.Time
	}{
		{name: "empty session id", sessionID: "", from: types.None[types.EventID](), to: "evt-to", at: now},
		{name: "empty to event id", sessionID: "sess", from: types.None[types.EventID](), to: "", at: now},
		{name: "zero observed_at", sessionID: "sess", from: types.None[types.EventID](), to: "evt-to", at: time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := model.NewSessionOrphanRange(tt.sessionID, tt.from, tt.to, tt.at, now); err == nil {
				t.Fatal("error = nil, want error")
			}
		})
	}
	t.Run("zero earliest_event_time", func(t *testing.T) {
		t.Parallel()
		if _, err := model.NewSessionOrphanRange("sess", types.None[types.EventID](), "evt-to", now, time.Time{}); err == nil {
			t.Fatal("error = nil, want error")
		}
	})
}
