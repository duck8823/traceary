package model_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewConsolidationRequest_EnforcesInvariants(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	valid, err := model.NewConsolidationRequest(
		types.SessionID("sess-1"),
		"claude",
		at,
		types.EventID("evt-1"),
		"body_bytes",
		1024,
		65536,
		false,
		types.ConsolidationDeliveryStopExit2,
	)
	if err != nil {
		t.Fatalf("NewConsolidationRequest() error = %v", err)
	}
	if valid.Client() != "claude" || valid.Signal() != "body_bytes" || valid.ReRequest() {
		t.Fatalf("unexpected request: %+v", valid)
	}

	tests := []struct {
		name      string
		sessionID types.SessionID
		client    string
		eventID   types.EventID
		signal    string
		pressure  int64
		threshold int64
		delivery  types.ConsolidationDelivery
	}{
		{name: "empty session id", client: "claude", eventID: "evt-1", signal: "body_bytes", threshold: 1, delivery: types.ConsolidationDeliveryStopExit2},
		{name: "empty client", sessionID: "sess-1", eventID: "evt-1", signal: "body_bytes", threshold: 1, delivery: types.ConsolidationDeliveryStopExit2},
		{name: "empty at event id", sessionID: "sess-1", client: "claude", signal: "body_bytes", threshold: 1, delivery: types.ConsolidationDeliveryStopExit2},
		{name: "empty signal", sessionID: "sess-1", client: "claude", eventID: "evt-1", threshold: 1, delivery: types.ConsolidationDeliveryStopExit2},
		{name: "non-positive threshold", sessionID: "sess-1", client: "claude", eventID: "evt-1", signal: "body_bytes", delivery: types.ConsolidationDeliveryStopExit2},
		{name: "negative pressure", sessionID: "sess-1", client: "claude", eventID: "evt-1", signal: "body_bytes", pressure: -1, threshold: 1, delivery: types.ConsolidationDeliveryStopExit2},
		{name: "unknown delivery", sessionID: "sess-1", client: "claude", eventID: "evt-1", signal: "body_bytes", threshold: 1, delivery: types.ConsolidationDelivery("stderr")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := model.NewConsolidationRequest(
				tt.sessionID, tt.client, at, tt.eventID, tt.signal, tt.pressure, tt.threshold, false, tt.delivery,
			)
			if err == nil {
				t.Fatal("NewConsolidationRequest() error = nil, want error")
			}
		})
	}
}
