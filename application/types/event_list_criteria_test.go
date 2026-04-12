package types_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventListCriteriaBuilder_DefaultsLimitOnly(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewEventListCriteriaBuilder(25).Build()

	if diff := cmp.Diff(25, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, criteria.Offset()); diff != "" {
		t.Errorf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.EventKind(""), criteria.Kind()); diff != "" {
		t.Errorf("Kind() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client(""), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent(""), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID(""), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace(""), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if criteria.FailuresOnly() {
		t.Errorf("FailuresOnly() = true, want false")
	}
	if !criteria.From().IsZero() {
		t.Errorf("From() = %v, want zero", criteria.From())
	}
	if !criteria.To().IsZero() {
		t.Errorf("To() = %v, want zero", criteria.To())
	}
}

func TestEventListCriteriaBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	from := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)

	criteria := apptypes.NewEventListCriteriaBuilder(100).
		Offset(30).
		Kind(domtypes.EventKindNote).
		Client(domtypes.Client("cli")).
		Agent(domtypes.Agent("codex")).
		SessionID(domtypes.SessionID("session-2")).
		Workspace(domtypes.Workspace("github.com/org/repo")).
		FailuresOnly(true).
		From(from).
		To(to).
		Build()

	if diff := cmp.Diff(100, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(30, criteria.Offset()); diff != "" {
		t.Errorf("Offset() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.EventKindNote, criteria.Kind()); diff != "" {
		t.Errorf("Kind() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client("cli"), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent("codex"), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID("session-2"), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("github.com/org/repo"), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.FailuresOnly() {
		t.Errorf("FailuresOnly() = false, want true")
	}
	if !criteria.From().Equal(from) {
		t.Errorf("From() = %v, want %v", criteria.From(), from)
	}
	if !criteria.To().Equal(to) {
		t.Errorf("To() = %v, want %v", criteria.To(), to)
	}
}
