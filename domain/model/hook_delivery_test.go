package model_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewHookDeliveryEvidence_SeparatesDeliveryAndAttribution(t *testing.T) {
	first := deliveryEvent(t, "/repo/a")
	second := deliveryEvent(t, "/repo/b")

	left, err := model.NewHookDeliveryEvidence(first, "tool-1", "/repo/a")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence(first) error = %v", err)
	}
	right, err := model.NewHookDeliveryEvidence(second, "tool-1", "/repo/b")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence(second) error = %v", err)
	}
	if left.ReportedID() != "codex:post_tool_use:session-1:tool-1" {
		t.Fatalf("ReportedID() = %q", left.ReportedID())
	}
	if left.DeliveryFingerprint() != right.DeliveryFingerprint() {
		t.Fatal("delivery fingerprint changed with workspace attribution")
	}
	if left.AttributionFingerprint() == right.AttributionFingerprint() {
		t.Fatal("attribution fingerprint did not change with workspace")
	}
}

func TestNewHookDeliveryEvidence_IncludesAdditionalSemanticFields(t *testing.T) {
	event := deliveryEvent(t, "/repo")
	success, err := model.NewHookDeliveryEvidence(event, "tool-1", "/repo", "audit", "failed=false")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence(success) error = %v", err)
	}
	failure, err := model.NewHookDeliveryEvidence(event, "tool-1", "/repo", "audit", "failed=true")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence(failure) error = %v", err)
	}
	if success.DeliveryFingerprint() == failure.DeliveryFingerprint() {
		t.Fatal("delivery fingerprint ignored additional semantic fields")
	}
}

func TestClassifyWorkspaceRelationship(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		effective string
		want      model.WorkspaceRelationship
	}{
		{name: "unknown", canonical: "", effective: "/repo", want: model.WorkspaceRelationshipUnknown},
		{name: "exact remote", canonical: "github.com/o/r", effective: "github.com/o/r", want: model.WorkspaceRelationshipExact},
		{name: "remote local conflict", canonical: "github.com/o/r", effective: "/repo", want: model.WorkspaceRelationshipConflict},
		{name: "descendant", canonical: "/repo", effective: "/repo/sub", want: model.WorkspaceRelationshipDescendant},
		{name: "ancestor", canonical: "/repo/sub", effective: "/repo", want: model.WorkspaceRelationshipAncestor},
		{name: "windows descendant", canonical: `C:\repo`, effective: `C:\repo\sub`, want: model.WorkspaceRelationshipDescendant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.ClassifyWorkspaceRelationship(types.Workspace(tt.canonical), types.Workspace(tt.effective))
			if got != tt.want {
				t.Fatalf("ClassifyWorkspaceRelationship() = %q, want %q", got, tt.want)
			}
		})
	}
}

func deliveryEvent(t *testing.T, workspace string) *model.Event {
	t.Helper()
	event, err := model.NewEvent(
		types.EventID("event-1"),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("session-1"),
		types.Workspace(workspace),
		"go test ./...",
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	event.SetSourceHook("post_tool_use")
	return event
}
