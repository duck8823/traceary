package types_test

import (
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventBodyExtentOf_RejectsInvalidStoredBytes(t *testing.T) {
	t.Parallel()

	if _, err := apptypes.EventBodyExtentOf(
		domtypes.None[int](),
		-1,
		domtypes.None[bool](),
		domtypes.None[bool](),
		domtypes.None[int](),
	); err == nil {
		t.Fatal("EventBodyExtentOf() error = nil, want invalid stored byte count")
	}
}

func TestEventMetadataOf_PreservesUnknownBodyFacts(t *testing.T) {
	t.Parallel()

	eventID, err := domtypes.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := domtypes.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := domtypes.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	extent, err := apptypes.EventBodyExtentOf(
		domtypes.None[int](),
		42,
		domtypes.None[bool](),
		domtypes.None[bool](),
		domtypes.None[int](),
	)
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}

	createdAt := time.Date(2026, 7, 22, 1, 2, 3, 0, time.UTC)
	metadata, err := apptypes.EventMetadataOf(
		eventID,
		domtypes.EventKindNote,
		domtypes.Client("cli"),
		agent,
		sessionID,
		domtypes.Workspace("duck8823/traceary"),
		"manual",
		createdAt,
		extent,
		domtypes.None[apptypes.CommandAuditMetadata](),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}

	if metadata.EventID() != eventID || metadata.CreatedAt() != createdAt {
		t.Fatalf("metadata identity = %s/%s, want %s/%s", metadata.EventID(), metadata.CreatedAt(), eventID, createdAt)
	}
	if _, ok := metadata.BodyExtent().OriginalBytes().Value(); ok {
		t.Fatal("OriginalBytes() unexpectedly present")
	}
	if got := metadata.BodyExtent().StoredBytes(); got != 42 {
		t.Fatalf("StoredBytes() = %d, want 42", got)
	}
	if _, ok := metadata.CommandAudit().Value(); ok {
		t.Fatal("CommandAudit() unexpectedly present")
	}
}
