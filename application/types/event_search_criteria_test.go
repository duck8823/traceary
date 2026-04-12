package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventSearchCriteriaBuilder_DefaultsLimitOnly(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewEventSearchCriteriaBuilder(50).Build()

	if diff := cmp.Diff(50, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", criteria.Query()); diff != "" {
		t.Errorf("Query() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace(""), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID(""), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client(""), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent(""), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.EventKind(""), criteria.Kind()); diff != "" {
		t.Errorf("Kind() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.From().IsZero() {
		t.Errorf("From() = %v, want zero", criteria.From())
	}
	if !criteria.To().IsZero() {
		t.Errorf("To() = %v, want zero", criteria.To())
	}
	if diff := cmp.Diff(0, criteria.Offset()); diff != "" {
		t.Errorf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if criteria.FailuresOnly() {
		t.Errorf("FailuresOnly() = true, want false")
	}
}

func TestEventSearchCriteriaBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	from := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)

	criteria := apptypes.NewEventSearchCriteriaBuilder(100).
		Query("needle").
		Workspace(domtypes.Workspace("github.com/org/repo")).
		SessionID(domtypes.SessionID("session-1")).
		Client(domtypes.Client("cli")).
		Agent(domtypes.Agent("claude")).
		Kind(domtypes.EventKindCommandExecuted).
		From(from).
		To(to).
		Offset(20).
		FailuresOnly(true).
		Build()

	if diff := cmp.Diff(100, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("needle", criteria.Query()); diff != "" {
		t.Errorf("Query() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("github.com/org/repo"), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID("session-1"), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client("cli"), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent("claude"), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.EventKindCommandExecuted, criteria.Kind()); diff != "" {
		t.Errorf("Kind() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.From().Equal(from) {
		t.Errorf("From() = %v, want %v", criteria.From(), from)
	}
	if !criteria.To().Equal(to) {
		t.Errorf("To() = %v, want %v", criteria.To(), to)
	}
	if diff := cmp.Diff(20, criteria.Offset()); diff != "" {
		t.Errorf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.FailuresOnly() {
		t.Errorf("FailuresOnly() = false, want true")
	}
}
