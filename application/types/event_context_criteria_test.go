package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestEventContextCriteriaBuilder_DefaultsLimitOnly(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewEventContextCriteriaBuilder(10).Build()

	if diff := cmp.Diff(10, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
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
}

func TestEventContextCriteriaBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewEventContextCriteriaBuilder(42).
		Workspace(domtypes.Workspace("ws-1")).
		SessionID(domtypes.SessionID("session-1")).
		Client(domtypes.Client("hook")).
		Agent(domtypes.Agent("claude")).
		Build()

	if diff := cmp.Diff(42, criteria.Limit()); diff != "" {
		t.Errorf("Limit() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("ws-1"), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.SessionID("session-1"), criteria.SessionID()); diff != "" {
		t.Errorf("SessionID() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Client("hook"), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent("claude"), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
}
