package domain

import (
	"reflect"
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

func TestPreparedStoreUpgradeRejectsRollbackBeforeOriginalIsPublished(t *testing.T) {
	run := PreparedStoreUpgradeRun{Phase: PreparedStoreUpgradeSwapped}
	if _, err := run.Advance(PreparedStoreUpgradeRollbackSwapIntent, time.Now()); err == nil {
		t.Fatal("Advance accepted rollback directly from swapped")
	}
}

func TestPreparedStoreUpgradeRecordsRetryBeforeOwnedCleanup(t *testing.T) {
	run := PreparedStoreUpgradeRun{
		Phase:                     PreparedStoreUpgradeCandidatePrepared,
		PreparedCandidateIdentity: StoreFileIdentity{Device: 1, Inode: 2},
	}
	actions, err := run.RecoveryActions(PreparedStoreUpgradeObservation{
		Orientation:        OrientationCandidateReady,
		Candidate:          run.PreparedCandidateIdentity,
		CandidateCondition: CandidateConditionOwnedIncomplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PreparedStoreUpgradeAction{ActionRecordCopyRetryIntent, ActionRemoveOwnedPartialCandidate}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %v, want %v", actions, want)
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
