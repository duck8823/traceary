package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestEvidenceRefFrom(t *testing.T) {
	t.Parallel()

	ref, err := types.EvidenceRefFrom(types.EvidenceRefKindEvent, "event-123")
	if err != nil {
		t.Fatalf("EvidenceRefFrom() error = %v", err)
	}
	if ref.Kind() != types.EvidenceRefKindEvent {
		t.Fatalf("Kind() = %v, want %v", ref.Kind(), types.EvidenceRefKindEvent)
	}
	if ref.Value() != "event-123" {
		t.Fatalf("Value() = %q, want %q", ref.Value(), "event-123")
	}
}

func TestEvidenceRefFrom_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := types.EvidenceRefFrom(types.EvidenceRefKind(""), "value"); err == nil {
		t.Fatalf("EvidenceRefFrom() error = nil, want error for empty kind")
	}
	if _, err := types.EvidenceRefFrom(types.EvidenceRefKindEvent, " "); err == nil {
		t.Fatalf("EvidenceRefFrom() error = nil, want error for empty value")
	}
}
