package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestSessionLookupCriteriaBuilder_Defaults(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewSessionLookupCriteriaBuilder().Build()

	if diff := cmp.Diff(domtypes.Client(""), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent(""), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace(""), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if criteria.ActiveOnly() {
		t.Errorf("ActiveOnly() = true, want false")
	}
}

func TestSessionLookupCriteriaBuilder_AllSettersChained(t *testing.T) {
	t.Parallel()

	criteria := apptypes.NewSessionLookupCriteriaBuilder().
		Client(domtypes.Client("mcp")).
		Agent(domtypes.Agent("claude")).
		Workspace(domtypes.Workspace("ws-a")).
		ActiveOnly(true).
		Build()

	if diff := cmp.Diff(domtypes.Client("mcp"), criteria.Client()); diff != "" {
		t.Errorf("Client() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Agent("claude"), criteria.Agent()); diff != "" {
		t.Errorf("Agent() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(domtypes.Workspace("ws-a"), criteria.Workspace()); diff != "" {
		t.Errorf("Workspace() mismatch (-want +got):\n%s", diff)
	}
	if !criteria.ActiveOnly() {
		t.Errorf("ActiveOnly() = false, want true")
	}
}
