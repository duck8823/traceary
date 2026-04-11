package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

type eventDetailsFinderStub struct {
	receivedEventID string
	eventDetails    *port.EventDetails
	err             error
}

func (s *eventDetailsFinderStub) GetEventDetails(
	_ context.Context,
	eventID string,
) (*port.EventDetails, error) {
	s.receivedEventID = eventID
	return s.eventDetails, s.err
}

func TestGetEventDetailsQueryService_Run(t *testing.T) {
	t.Parallel()

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

	t.Run("returns event details", func(t *testing.T) {
		t.Parallel()

		eventDetails, err := port.NewEventDetails(
			model.EventOf(
				eventID,
				types.EventKindCommandExecuted,
				"cli",
				agent,
				sessionID,
				"github.com/duck8823/traceary",
				"go test ./...",
				time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
			),
			model.CommandAuditOf(
				eventID,
				"go test ./...",
				"stdin",
				"stdout",
				false,
				false,
				nil,
			),
		)
		if err != nil {
			t.Fatalf("NewEventDetails() error = %v", err)
		}
		stub := &eventDetailsFinderStub{eventDetails: eventDetails}
		sut := queryservice.NewGetEventDetailsQueryService(stub)

		got, err := sut.Run(context.Background(), "event-1")
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedEventID != "event-1" {
			t.Fatalf("received event ID = %q, want %q", stub.receivedEventID, "event-1")
		}
		if got.Event().EventID() != eventID {
			t.Fatalf("EventID() = %q, want %q", got.Event().EventID(), eventID)
		}
		if got.CommandAudit() == nil {
			t.Fatalf("CommandAudit() = nil, want command audit")
		}
	})

	t.Run("event ID が空ならエラー", func(t *testing.T) {
		t.Parallel()

		sut := queryservice.NewGetEventDetailsQueryService(&eventDetailsFinderStub{})

		_, err := sut.Run(context.Background(), "")
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
