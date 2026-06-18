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
	base := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	details := []apptypes.EventDetails{
		mustAuditDetailForReliability(t, "evt-a", "session-1", "workspace-1", "go test ./...", `{"command":"go test ./..."}`, largeOutput, base),
		mustAuditDetailForReliability(t, "evt-b", "session-1", "workspace-1", "go test ./...", `{"command":"go test ./..."}`, largeOutput, base.Add(time.Second)),
	}

	findings := commandAuditReliabilityFindingsFromDetails(context.Background(), details, false)
	if findings.ScannedAuditCount != 2 {
		t.Fatalf("ScannedAuditCount = %d, want 2", findings.ScannedAuditCount)
	}
	if len(findings.DuplicateGroups) != 1 {
		t.Fatalf("DuplicateGroups = %+v, want one group", findings.DuplicateGroups)
	}
	if findings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("duplicate count = %d, want 2", findings.DuplicateGroups[0].Count)
	}

	check := commandAuditReliabilityCheckFromFindings(findings, false)
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

// TestCommandAuditReliabilityFindingsIgnoreIntentionalRerunsByDefault covers the
// #1168 false positive: the same command intentionally re-run minutes apart
// during a review/merge flow must NOT be flagged by default.
func TestCommandAuditReliabilityFindingsIgnoreIntentionalRerunsByDefault(t *testing.T) {
	base := time.Date(2026, 6, 6, 4, 30, 51, 0, time.UTC)
	command := "rtk gh pr checks 1147 --json name,state,workflow,bucket,link"
	details := []apptypes.EventDetails{
		mustAuditDetailForReliability(t, "evt-1", "session-1", "workspace-1", command, `{"command":"checks"}`, "ok", base),
		mustAuditDetailForReliability(t, "evt-2", "session-1", "workspace-1", command, `{"command":"checks"}`, "ok", base.Add(9*time.Minute)),
		mustAuditDetailForReliability(t, "evt-3", "session-1", "workspace-1", command, `{"command":"checks"}`, "ok", base.Add(14*time.Minute)),
	}

	findings := commandAuditReliabilityFindingsFromDetails(context.Background(), details, false)
	if len(findings.DuplicateGroups) != 0 {
		t.Fatalf("DuplicateGroups = %+v, want none for minute-apart re-runs", findings.DuplicateGroups)
	}
	check := commandAuditReliabilityCheckFromFindings(findings, false)
	if check.Status != doctorStatusPass {
		t.Fatalf("check status = %q, want pass", check.Status)
	}
}

// TestCommandAuditReliabilityFindingsFlagNearSimultaneousDuplicates confirms a
// near-simultaneous identity match (likely hook double-write) is still flagged.
func TestCommandAuditReliabilityFindingsFlagNearSimultaneousDuplicates(t *testing.T) {
	base := time.Date(2026, 6, 6, 4, 30, 51, 0, time.UTC)
	details := []apptypes.EventDetails{
		mustAuditDetailForReliability(t, "evt-1", "session-1", "workspace-1", "git status", `{"command":"git status"}`, "ok", base),
		mustAuditDetailForReliability(t, "evt-2", "session-1", "workspace-1", "git status", `{"command":"git status"}`, "ok", base.Add(time.Second)),
	}

	findings := commandAuditReliabilityFindingsFromDetails(context.Background(), details, false)
	if len(findings.DuplicateGroups) != 1 || findings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("DuplicateGroups = %+v, want one near-simultaneous group of 2", findings.DuplicateGroups)
	}
}

// TestCommandAuditReliabilityFindingsSplitNearAndFarDuplicates verifies that
// within one identity group only the near-simultaneous cluster is flagged by
// default, while strict mode reports the whole exact group.
func TestCommandAuditReliabilityFindingsSplitNearAndFarDuplicates(t *testing.T) {
	base := time.Date(2026, 6, 6, 4, 30, 51, 0, time.UTC)
	command := "go test ./..."
	newDetails := func() []apptypes.EventDetails {
		return []apptypes.EventDetails{
			mustAuditDetailForReliability(t, "evt-near-1", "session-1", "workspace-1", command, `{"command":"test"}`, "ok", base),
			mustAuditDetailForReliability(t, "evt-near-2", "session-1", "workspace-1", command, `{"command":"test"}`, "ok", base.Add(time.Second)),
			mustAuditDetailForReliability(t, "evt-far", "session-1", "workspace-1", command, `{"command":"test"}`, "ok", base.Add(10*time.Minute)),
		}
	}

	defaultFindings := commandAuditReliabilityFindingsFromDetails(context.Background(), newDetails(), false)
	if len(defaultFindings.DuplicateGroups) != 1 || defaultFindings.DuplicateGroups[0].Count != 2 {
		t.Fatalf("default DuplicateGroups = %+v, want only the near-simultaneous pair", defaultFindings.DuplicateGroups)
	}

	strictFindings := commandAuditReliabilityFindingsFromDetails(context.Background(), newDetails(), true)
	if len(strictFindings.DuplicateGroups) != 1 || strictFindings.DuplicateGroups[0].Count != 3 {
		t.Fatalf("strict DuplicateGroups = %+v, want one exact group of 3", strictFindings.DuplicateGroups)
	}
}

func TestCommandAuditReliabilityFindingsDetectWorkspaceDrift(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "traceary")
	base := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	details := []apptypes.EventDetails{
		mustAuditDetailForReliability(t, "evt-drift", "session-1", "stored-workspace", "pwd", `{"cwd":`+quoteJSONStringForReliability(cwd)+`}`, "ok", base),
	}

	findings := commandAuditReliabilityFindingsFromDetails(context.Background(), details, false)
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

func mustAuditDetailForReliability(t *testing.T, eventID, sessionID, workspace, command, input, output string, createdAt time.Time) apptypes.EventDetails {
	t.Helper()
	event := model.EventOf(
		types.EventID(eventID),
		types.EventKindCommandExecuted,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID(sessionID),
		types.Workspace(workspace),
		"command executed",
		createdAt,
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
