package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestSessionListCriteriaBuilder_DefaultsLimitOnly(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewSessionListCriteriaBuilder(25).Build()

	if diff := cmp.Diff(25, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, criteria.Offset()); diff != "" {
		t.Errorf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID(""), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace(""), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client(""), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent(""), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", criteria.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}
	if _, ok := criteria.From().Value(); ok {
		t.Errorf("From().Value() = true, want false")
	}
	if _, ok := criteria.To().Value(); ok {
		t.Errorf("To().Value() = true, want false")
	}
}

func TestSessionListCriteriaBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	from := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)

	criteria := apptypes.NewSessionListCriteriaBuilder(100).
		Offset(50).
		SessionID(domtypes.SessionID("session-1")).
		Workspace(domtypes.Workspace("ws")).
		Client(domtypes.Client("cli")).
		Agent(domtypes.Agent("claude")).
		Label("feature/foo").
		From(domtypes.Some(from)).
		To(domtypes.Some(to)).
		Build()

	if diff := cmp.Diff(100, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(50, criteria.Offset()); diff != "" {
		t.Errorf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID("session-1"), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("ws"), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client("cli"), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent("claude"), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("feature/foo", criteria.Label()); diff != "" {
		t.Errorf("Label() mismatch (-want +got):\n%s", diff)
	}

	gotFrom, ok := criteria.From().Value()
	if !ok {
		t.Fatalf("From().Value() ok = false, want true")
	}
	if !gotFrom.Equal(from) {
		t.Errorf("From() = %v, want %v", gotFrom, from)
	}
	gotTo, ok := criteria.To().Value()
	if !ok {
		t.Fatalf("To().Value() ok = false, want true")
	}
	if !gotTo.Equal(to) {
		t.Errorf("To() = %v, want %v", gotTo, to)
	}
}
