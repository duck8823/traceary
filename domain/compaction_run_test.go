package domain

import (
	"testing"
	"time"
)

func TestCompactionRunAdvanceRejectsSkippedTransition(t *testing.T) {
	run := CompactionRun{Phase: CompactionPlanned}
	if _, err := run.Advance(CompactionCandidateVerified, time.Now()); err == nil {
		t.Fatal("Advance() accepted skipped transition")
	}
	got, err := run.Advance(CompactionCopyIntent, time.Now())
	if err != nil || got.Phase != CompactionCopyIntent {
		t.Fatalf("Advance() = %q, %v", got.Phase, err)
	}
}

func TestCompactionRunRecoveryActionsRejectUnknownAndDecideCrashRecovery(t *testing.T) {
	run := CompactionRun{Phase: CompactionSwapIntent}
	actions, err := run.RecoveryActions(CompactionObservation{Orientation: OrientationSwapped})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || actions[0] != ActionSyncRecoveredOrientation || actions[1] != ActionRecordSwapped {
		t.Fatalf("actions=%v", actions)
	}
	if _, err := run.RecoveryActions(CompactionObservation{Orientation: OrientationSourceOriginal}); err == nil {
		t.Fatal("accepted impossible source orientation")
	}
}

func TestCompactionRunNextActionOwnsNormalDecision(t *testing.T) {
	action, err := (CompactionRun{Phase: CompactionScrubInProgress}).NextAction()
	if err != nil {
		t.Fatal(err)
	}
	if action != ActionVerifyCandidate {
		t.Fatalf("action=%s", action)
	}
}
