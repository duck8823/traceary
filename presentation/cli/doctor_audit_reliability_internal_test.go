package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestCommandAuditReliabilityFindingsDetectDuplicateGroups(t *testing.T) {
	largeOutput := strings.Repeat("x", 2048)
	details := []apptypes.EventDetails{
		mustAuditDetailForReliability(t, "evt-a", "session-1", "workspace-1", "go test ./...", `{"command":"go test ./..."}`, largeOutput),
		mustAuditDetailForReliability(t, "evt-b", "session-1", "workspace-1", "go test ./...", `{"command":"go test ./..."}`, largeOutput),
	}

	findings := commandAuditReliabilityFindingsFromDetails(context.Background(), details)
	if findings.ScannedAuditCount != 2 {
		t.Fatalf("ScannedAuditCount = %d, want 2", findings.ScannedAuditCount)
	}
	if len(findings.DuplicateGroups) != 1 {
		t.Fatalf("DuplicateGroups = %+v, want one group", findings.DuplicateGroups)
	}
	if findings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("duplicate count = %d, want 2", findings.DuplicateGroups[0].Count)
	}

	check := commandAuditReliabilityCheckFromFindings(findings)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check status = %q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "evt-a") || !strings.Contains(check.Message, "evt-b") {
		t.Fatalf("check message = %q, want sampled event IDs", check.Message)
	}
	if strings.Contains(check.Message, largeOutput) {
		t.Fatalf("check message leaked full audit output body")
	}
}

func TestCommandAuditReliabilityFindingsDetectWorkspaceDrift(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "traceary")
	details := []apptypes.EventDetails{
		mustAuditDetailForReliability(t, "evt-drift", "session-1", "stored-workspace", "pwd", `{"cwd":`+quoteJSONStringForReliability(cwd)+`}`, "ok"),
	}

	findings := commandAuditReliabilityFindingsFromDetails(context.Background(), details)
	if len(findings.WorkspaceDriftSamples) != 1 {
		t.Fatalf("WorkspaceDriftSamples = %+v, want one drift candidate", findings.WorkspaceDriftSamples)
	}
	drift := findings.WorkspaceDriftSamples[0]
	if drift.EventID != "evt-drift" || drift.StoredWorkspace != "stored-workspace" {
		t.Fatalf("drift sample = %+v", drift)
	}
	if drift.EvidenceWorkspace == "" || drift.EvidenceWorkspace == "stored-workspace" {
		t.Fatalf("EvidenceWorkspace = %q, want cwd-derived workspace different from stored", drift.EvidenceWorkspace)
	}
}

func mustAuditDetailForReliability(t *testing.T, eventID, sessionID, workspace, command, input, output string) apptypes.EventDetails {
	t.Helper()
	event := model.EventOf(
		types.EventID(eventID),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		"command executed",
		time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	)
	audit := model.CommandAuditOf(
		types.EventID(eventID),
		command,
		input,
		output,
		false,
		false,
		types.None[int](),
		false,
	)
	details, err := apptypes.EventDetailsOf(event, types.Some(audit))
	if err != nil {
		t.Fatalf("EventDetailsOf() error = %v", err)
	}
	return details
}

func quoteJSONStringForReliability(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
