package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestEvidenceRefOf(t *testing.T) {
	t.Parallel()

	ref, err := types.EvidenceRefOf(types.EvidenceRefKindEvent, "event-123")
	if err != nil {
		t.Fatalf("EvidenceRefOf() error = %v", err)
	}
	if ref.Kind() != types.EvidenceRefKindEvent {
		t.Fatalf("Kind() = %v, want %v", ref.Kind(), types.EvidenceRefKindEvent)
	}
	if ref.Value() != "event-123" {
		t.Fatalf("Value() = %q, want %q", ref.Value(), "event-123")
	}
}

func TestEvidenceRefOf_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := types.EvidenceRefOf(types.EvidenceRefKind(""), "value"); err == nil {
		t.Fatalf("EvidenceRefOf() error = nil, want error for empty kind")
	}
	if _, err := types.EvidenceRefOf(types.EvidenceRefKindEvent, " "); err == nil {
		t.Fatalf("EvidenceRefOf() error = nil, want error for empty value")
	}
}
