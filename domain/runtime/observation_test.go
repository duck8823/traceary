package runtime_test

import (
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
	"testing"
)

func TestDerivedObservationIsClosedBoundedAndMetadataOnly(t *testing.T) {
	valid := domain.DerivedObservationState{Host: "codex", HostVersion: "fixture 1", EventID: "spool:0123456789abcdef", Kind: domain.ObservationPromptBoundary}
	for _, tt := range []struct {
		name   string
		mutate func(*domain.DerivedObservationState)
	}{
		{"unknown_host", func(s *domain.DerivedObservationState) { s.Host = "muse" }},
		{"unknown_kind", func(s *domain.DerivedObservationState) { s.Kind = "approval_request" }},
		{"empty_event", func(s *domain.DerivedObservationState) { s.EventID = "" }},
		{"oversized_event", func(s *domain.DerivedObservationState) { s.EventID = strings.Repeat("e", 257) }},
		{"body_shaped_id", func(s *domain.DerivedObservationState) { s.NativeSessionID = "some prompt body" }},
		{"control_in_id", func(s *domain.DerivedObservationState) { s.NativeTurnID = "turn\nbody" }},
		{"empty_version", func(s *domain.DerivedObservationState) { s.HostVersion = "" }},
		{"oversized_version", func(s *domain.DerivedObservationState) { s.HostVersion = strings.Repeat("v", 257) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := valid
			tt.mutate(&s)
			if _, err := domain.DerivedObservationOf(s); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid metadata error = %v", err)
			}
		})
	}
	for _, kind := range []domain.ObservationKind{domain.ObservationPromptBoundary, domain.ObservationToolBoundary, domain.ObservationTurnBoundary, domain.ObservationSessionBoundary} {
		s := valid
		s.Kind = kind
		value, err := domain.DerivedObservationOf(s)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := value.Snapshot()
		snapshot.EventID = "changed"
		if value.Snapshot() != s {
			t.Fatal("mutable or altered metadata snapshot")
		}
	}
}
