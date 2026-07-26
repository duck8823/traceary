package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventPageAnchorOf(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 26, 1, 2, 3, 456, time.FixedZone("test", 9*60*60))
	anchor, err := apptypes.EventPageAnchorOf(createdAt, domtypes.EventID("event-1"))
	if err != nil {
		t.Fatalf("EventPageAnchorOf() error = %v", err)
	}
	if anchor.IsZero() {
		t.Fatal("EventPageAnchorOf() returned a zero anchor")
	}
	if !anchor.CreatedAt().Equal(createdAt) || anchor.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt() = %s (%s), want UTC %s", anchor.CreatedAt(), anchor.CreatedAt().Location(), createdAt.UTC())
	}
	if anchor.EventID() != "event-1" {
		t.Fatalf("EventID() = %q, want event-1", anchor.EventID())
	}
}

func TestEventPageAnchorOfRejectsIncompleteTuple(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		createdAt time.Time
		eventID   domtypes.EventID
	}{
		{name: "zero created-at", eventID: "event-1"},
		{name: "empty event ID", createdAt: time.Now()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := apptypes.EventPageAnchorOf(tt.createdAt, tt.eventID); err == nil {
				t.Fatal("EventPageAnchorOf() error = nil")
			}
		})
	}
}
