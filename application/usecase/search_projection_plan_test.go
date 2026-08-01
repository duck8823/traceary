package usecase_test

import (
	"errors"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestProjectionBatchPlanEnforcesCombinedLogicalAndWALHardCap(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 10, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1000, DecodedBytes: 1000, WriteBytes: 100, RecentAge: time.Hour, RecentBytes: 1000}
	s := apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1}, Phase: "source", Now: time.Now(), Documents: []apptypes.ProjectionDocument{{Sequence: 1, Text: "small", StoredBytes: 5, DecodedBytes: 5}}}
	_, err := usecase.PlanProjectionBatch(s, b)
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) {
		t.Fatalf("error = %T %v, want hard-cap no progress", err, err)
	}
	b.WriteBytes = 1000
	p, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Ledger.LogicalWriteBytes + p.Ledger.WALReservationBytes; got > b.WriteBytes {
		t.Fatalf("reserved bytes = %d > %d", got, b.WriteBytes)
	}
}

func TestProjectionRetentionPlanRequiresPersistedResumeWhenSnapshotHasMoreRows(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, RecentBytes: 100}
	s := apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g"}, Phase: "cleanup", Cleanup: []apptypes.ProjectionCleanupCandidate{{Class: "summary", RowID: 1, LogicalBytes: 10}}, CleanupDone: false}
	p, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Completed || p.NextPhase != "" {
		t.Fatalf("premature completion: %+v", p)
	}
	s.CleanupDone = true
	p, err = usecase.PlanProjectionBatch(s, b)
	if err != nil || !p.Completed {
		t.Fatalf("final cleanup: %+v %v", p, err)
	}
}
