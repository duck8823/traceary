package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type wallBudgetStore struct {
	budget  apptypes.SearchProjectionBudget
	applied bool
}

func (s *wallBudgetStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	return apptypes.SearchProjectionGeneration{}, nil
}
func (s *wallBudgetStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: "rebuilding", ConfigHash: s.budget.ConfigHash()}, nil
}

//nolint:wrapcheck // Test fake preserves context cancellation identity.
func (s *wallBudgetStore) SelectSnapshot(ctx context.Context, _ apptypes.SearchProjectionBudget, _ time.Time) (apptypes.ProjectionSnapshot, error) {
	<-ctx.Done()
	return apptypes.ProjectionSnapshot{}, ctx.Err()
}
func (s *wallBudgetStore) ApplyBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error) {
	s.applied = true
	return apptypes.SearchProjectionProgress{}, nil
}
func (s *wallBudgetStore) CleanupBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error) {
	s.applied = true
	return apptypes.SearchProjectionProgress{}, nil
}
func (*wallBudgetStore) MarkFailed(context.Context, string, int64, string, time.Time) error {
	return nil
}

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

func TestProjectionOperatorResultsUseSnakeCaseJSON(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(apptypes.SearchProjectionProgress{GenerationID: "g", StoredBytes: 1, CleanupBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"generation_id"`, `"stored_bytes"`, `"cleanup_bytes"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json=%s missing %s", got, want)
		}
	}
}

func TestResumeWallBudgetCoversSelectionAndPreventsApplyAfterDeadline(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, RecentBytes: 1}
	store := &wallBudgetStore{budget: b}
	_, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), b, time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if store.applied {
		t.Fatal("apply called after wall deadline")
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
