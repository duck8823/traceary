package model_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestCommandAuditClassifyOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		exitCode   types.Optional[int]
		reported   types.CommandFailureReason
		failed     bool
		wantReason types.CommandFailureReason
		wantFailed bool
	}{
		{name: "zero overrides quoted failure marker", exitCode: types.Some(0), reported: types.CommandFailureReasonHostError, failed: true, wantReason: types.CommandFailureReasonNone},
		{name: "non-zero exit", exitCode: types.Some(7), wantReason: types.CommandFailureReasonExitCode, wantFailed: true},
		{name: "signal with process code", exitCode: types.Some(143), reported: types.CommandFailureReasonSignal, wantReason: types.CommandFailureReasonSignal, wantFailed: true},
		{name: "timeout", reported: types.CommandFailureReasonTimeout, wantReason: types.CommandFailureReasonTimeout, wantFailed: true},
		{name: "hook denial", reported: types.CommandFailureReasonHookDenied, wantReason: types.CommandFailureReasonHookDenied, wantFailed: true},
		{name: "generic structural failure", failed: true, wantReason: types.CommandFailureReasonHostError, wantFailed: true},
		{name: "unreported outcome", wantReason: types.CommandFailureReasonUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			audit, err := model.NewCommandAudit("event", "rtk git status", "", `{"failed": true}`, false, false)
			if err != nil {
				t.Fatalf("NewCommandAudit() error = %v", err)
			}
			if err := audit.ClassifyOutcome(tc.exitCode, tc.reported, tc.failed); err != nil {
				t.Fatalf("ClassifyOutcome() error = %v", err)
			}
			if audit.FailureReason() != tc.wantReason {
				t.Fatalf("FailureReason() = %q, want %q", audit.FailureReason(), tc.wantReason)
			}
			if audit.Failed() != tc.wantFailed {
				t.Fatalf("Failed() = %v, want %v", audit.Failed(), tc.wantFailed)
			}
		})
	}
}

func TestCommandAuditFromSnapshotPreservesLegacyUnknowns(t *testing.T) {
	t.Parallel()
	audit, err := model.CommandAuditFromSnapshot(model.CommandAuditSnapshot{
		EventID:       "legacy",
		Command:       "rtk git status",
		CommandName:   types.CommandNameUnknown,
		Failed:        true,
		FailureReason: types.CommandFailureReasonUnknown,
	})
	if err != nil {
		t.Fatalf("CommandAuditFromSnapshot() error = %v", err)
	}
	if audit.CommandIdentity().Command() != types.CommandNameUnknown {
		t.Fatalf("legacy command name = %q, want unknown", audit.CommandIdentity().Command())
	}
	if _, ok := audit.CommandIdentity().Wrapper().Value(); ok {
		t.Fatal("legacy wrapper unexpectedly present")
	}
	if !audit.Failed() || audit.FailureReason() != types.CommandFailureReasonUnknown {
		t.Fatalf("legacy outcome = failed:%v reason:%q", audit.Failed(), audit.FailureReason())
	}
}

func TestCommandAuditFromSnapshotRejectsContradictorySuccess(t *testing.T) {
	t.Parallel()
	_, err := model.CommandAuditFromSnapshot(model.CommandAuditSnapshot{
		EventID:       "event",
		Command:       "go test ./...",
		CommandName:   "go",
		ExitCode:      types.Some(0),
		Failed:        true,
		FailureReason: types.CommandFailureReasonExitCode,
	})
	if err == nil || !strings.Contains(err.Error(), "exit code zero") {
		t.Fatalf("CommandAuditFromSnapshot() error = %v, want exit-code-zero contradiction", err)
	}
}
