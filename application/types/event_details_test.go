package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventDetailsOf_HappyPath(t *testing.T) {
	t.Parallel()

	event := model.EventOf(
		domtypes.EventID("evt-1"),
		domtypes.EventKindNote,
		domtypes.Client("cli"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("github.com/org/repo"),
		"event body",
		time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	audit := model.CommandAuditOf(
		domtypes.EventID("evt-1"),
		"echo hello",
		"input",
		"output",
		false,
		false,
		domtypes.Of(0),
	)
	auditOpt := domtypes.Of(audit)

	details, err := apptypes.EventDetailsOf(event, auditOpt)
	if err != nil {
		t.Fatalf("EventDetailsOf() returned unexpected error: %v", err)
	}

	if got := details.Event(); got != event {
		t.Errorf("Event() pointer mismatch: got %p, want %p", got, event)
	}

	gotAudit, ok := details.CommandAudit().Get()
	if !ok {
		t.Fatalf("CommandAudit().Get() ok = false, want true")
	}
	if gotAudit != audit {
		t.Errorf("CommandAudit() pointer mismatch: got %p, want %p", gotAudit, audit)
	}
}

func TestEventDetailsOf_EmptyAudit(t *testing.T) {
	t.Parallel()

	event := model.EventOf(
		domtypes.EventID("evt-2"),
		domtypes.EventKindNote,
		domtypes.Client("cli"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-2"),
		domtypes.Workspace("ws"),
		"body",
		time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
	)

	details, err := apptypes.EventDetailsOf(event, domtypes.Empty[*model.CommandAudit]())
	if err != nil {
		t.Fatalf("EventDetailsOf() returned unexpected error: %v", err)
	}

	if details.CommandAudit().IsPresent() {
		t.Errorf("CommandAudit().IsPresent() = true, want false")
	}
}

func TestEventDetailsOf_NilEventReturnsError(t *testing.T) {
	t.Parallel()

	details, err := apptypes.EventDetailsOf(nil, domtypes.Empty[*model.CommandAudit]())
	if err == nil {
		t.Fatalf("EventDetailsOf() with nil event returned nil error")
	}

	if details.Event() != nil {
		t.Errorf("Event() = %v, want nil on error", details.Event())
	}
	if details.CommandAudit().IsPresent() {
		t.Errorf("CommandAudit().IsPresent() = true, want false on error")
	}
}
