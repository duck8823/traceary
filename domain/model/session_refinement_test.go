package model_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewSessionRefinement_EnforcesInvariants(t *testing.T) {
	t.Parallel()

	producedAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	valid, err := model.NewSessionRefinement(
		types.SessionID("sess-1"),
		1,
		types.EventID("evt-from"),
		types.EventID("evt-to"),
		"summary text",
		"kw1,kw2",
		"agent",
		producedAt,
		false,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement() error = %v", err)
	}
	if diff := cmp.Diff(1, valid.Generation()); diff != "" {
		t.Fatalf("Generation mismatch (-want +got):\n%s", diff)
	}
	if !valid.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = false on non-degraded NewSessionRefinement, want true")
	}
	if valid.Degraded() {
		t.Fatal("Degraded() = true, want false")
	}

	tests := []struct {
		name       string
		generation int
		summary    string
		producedBy string
		from       types.EventID
		to         types.EventID
	}{
		{name: "generation zero", generation: 0, summary: "s", producedBy: "p", from: "a", to: "b"},
		{name: "empty summary", generation: 1, summary: "  ", producedBy: "p", from: "a", to: "b"},
		{name: "empty produced_by", generation: 1, summary: "s", producedBy: "", from: "a", to: "b"},
		{name: "empty covers_from", generation: 1, summary: "s", producedBy: "p", from: "", to: "b"},
		{name: "empty covers_to", generation: 1, summary: "s", producedBy: "p", from: "a", to: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := model.NewSessionRefinement(
				types.SessionID("sess-1"),
				tt.generation,
				tt.from,
				tt.to,
				tt.summary,
				"",
				tt.producedBy,
				producedAt,
				false,
			)
			if err == nil {
				t.Fatal("NewSessionRefinement() error = nil, want error")
			}
		})
	}
}

func TestNewSessionRefinement_DegradedDefaultsHasAgentReasoningFalse(t *testing.T) {
	t.Parallel()

	row, err := model.NewSessionRefinement(
		types.SessionID("sess-1"),
		1,
		types.EventID("evt-from"),
		types.EventID("evt-to"),
		"mechanical",
		"",
		"gc:orphan-consolidation",
		time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC),
		true,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement() error = %v", err)
	}
	if !row.Degraded() {
		t.Fatal("Degraded() = false, want true")
	}
	if row.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = true on degraded first write, want false")
	}
}

func TestSessionRefinementOf_RestoresHasAgentReasoningIndependently(t *testing.T) {
	t.Parallel()

	producedAt := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	mixed, err := model.SessionRefinementOf(
		types.SessionID("sess-1"),
		2,
		types.EventID("evt-from"),
		types.EventID("evt-to"),
		"agent prose\n\n---\n[degraded section]",
		"kw",
		"gc:orphan-consolidation",
		producedAt,
		true,
		true,
	)
	if err != nil {
		t.Fatalf("SessionRefinementOf() error = %v", err)
	}
	if !mixed.Degraded() {
		t.Fatal("Degraded() = false, want true")
	}
	if !mixed.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = false on mixed restored row, want true")
	}

	clone := mixed.WithHasAgentReasoning(false)
	if clone.HasAgentReasoning() {
		t.Fatal("WithHasAgentReasoning(false) did not clear the flag")
	}
	if !mixed.HasAgentReasoning() {
		t.Fatal("WithHasAgentReasoning must not mutate the original")
	}
}
