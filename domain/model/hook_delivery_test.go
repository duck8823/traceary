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
	if left.ReportedID() != "5:codex|13:post_tool_use|9:session-1|6:tool-1" {
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

func TestNewHookDeliveryEvidence_LengthPrefixesSemanticFields(t *testing.T) {
	event := deliveryEvent(t, "/repo")
	left, err := model.NewHookDeliveryEvidence(event, "tool-1", "/repo", "a\x00b", "c")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence(left) error = %v", err)
	}
	right, err := model.NewHookDeliveryEvidence(event, "tool-1", "/repo", "a", "b", "c")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence(right) error = %v", err)
	}
	if left.DeliveryFingerprint() == right.DeliveryFingerprint() {
		t.Fatal("delivery fingerprint allowed delimiter ambiguity")
	}
}

func TestNewHookDeliveryEvidence_PreservesRawWorkspace(t *testing.T) {
	event := deliveryEvent(t, "/repo/with-space ")
	evidence, err := model.NewHookDeliveryEvidence(event, "tool-1", " /repo/with-space ")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence() error = %v", err)
	}
	if evidence.RawWorkspace() != " /repo/with-space " {
		t.Fatalf("RawWorkspace() = %q, want exact host evidence", evidence.RawWorkspace())
	}
	if got := model.WorkspaceAttributionFingerprint(event.Workspace(), "/repo/with-space"); got == evidence.AttributionFingerprint() {
		t.Fatal("attribution fingerprint ignored raw workspace bytes")
	}
}

func TestNewHookDeliveryEvidence_ReportedIDIsUnambiguousAcrossIndependentDeliveries(t *testing.T) {
	tests := []struct {
		name  string
		left  deliveryIdentity
		right deliveryIdentity
	}{
		{
			name:  "colon shifts between session and native ID",
			left:  deliveryIdentity{agent: "codex", hook: "post_tool_use", sessionID: "session:a", nativeID: "tool-1"},
			right: deliveryIdentity{agent: "codex", hook: "post_tool_use", sessionID: "session", nativeID: "a:tool-1"},
		},
		{
			name:  "colon shifts between host and hook kind",
			left:  deliveryIdentity{agent: "codex:exec", hook: "stop", sessionID: "session-1", nativeID: "tool-1"},
			right: deliveryIdentity{agent: "codex", hook: "exec:stop", sessionID: "session-1", nativeID: "tool-1"},
		},
		{
			name:  "colon shifts between hook kind and session",
			left:  deliveryIdentity{agent: "codex", hook: "post:tool", sessionID: "use", nativeID: "tool-1"},
			right: deliveryIdentity{agent: "codex", hook: "post", sessionID: "tool:use", nativeID: "tool-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := tt.left.evidence(t)
			right := tt.right.evidence(t)
			if left.ReportedID() == right.ReportedID() {
				t.Fatalf("ReportedID() collided for independent deliveries: %q", left.ReportedID())
			}
			if left.DeliveryRecordID() == right.DeliveryRecordID() {
				t.Fatalf("DeliveryRecordID() collided for independent deliveries: %q", left.DeliveryRecordID())
			}
		})
	}
}

func TestNewHookDeliveryEvidence_ReportedIDStaysStableForOneLogicalDelivery(t *testing.T) {
	identity := deliveryIdentity{agent: "codex", hook: "post_tool_use", sessionID: "session-1", nativeID: "event_id:delivery-1"}
	first := identity.evidence(t)
	second := identity.evidence(t)
	if first.ReportedID() != second.ReportedID() {
		t.Fatalf("ReportedID() changed for one logical delivery: %q != %q", first.ReportedID(), second.ReportedID())
	}
	if first.DeliveryRecordID() != second.DeliveryRecordID() {
		t.Fatalf("DeliveryRecordID() changed for one logical delivery: %q != %q", first.DeliveryRecordID(), second.DeliveryRecordID())
	}
}

type deliveryIdentity struct {
	agent     string
	hook      string
	sessionID string
	nativeID  string
}

func (d deliveryIdentity) evidence(t *testing.T) model.HookDeliveryEvidence {
	t.Helper()
	event, err := model.NewEvent(
		types.EventID("event-1"),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent(d.agent),
		types.SessionID(d.sessionID),
		types.Workspace("/repo"),
		"go test ./...",
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	event.SetSourceHook(d.hook)
	evidence, err := model.NewHookDeliveryEvidence(event, d.nativeID, "/repo")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence() error = %v", err)
	}
	return evidence
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
		{name: "windows case insensitive", canonical: `C:\Repo`, effective: `c:\repo\Sub`, want: model.WorkspaceRelationshipDescendant},
		{name: "UNC case insensitive", canonical: `\\Server\Share\Repo`, effective: `\\server\share\repo`, want: model.WorkspaceRelationshipExact},
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
