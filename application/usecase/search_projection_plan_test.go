package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type wallBudgetStore struct {
	budget      apptypes.SearchProjectionBudget
	applied     bool
	delayStatus bool
	selected    bool
}

func (s *wallBudgetStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	return apptypes.SearchProjectionGeneration{}, nil
}

//nolint:wrapcheck // Test fake preserves context cancellation identity.
func (s *wallBudgetStore) SearchProjectionStatus(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	if s.delayStatus {
		<-ctx.Done()
		return apptypes.SearchProjectionStatus{}, ctx.Err()
	}
	return apptypes.SearchProjectionStatus{State: "rebuilding", ConfigHash: s.budget.ConfigHash()}, nil
}

//nolint:wrapcheck // Test fake preserves context cancellation identity.
func (s *wallBudgetStore) SelectSnapshot(ctx context.Context, _ apptypes.SearchProjectionBudget, _ time.Time) (apptypes.ProjectionSnapshot, error) {
	s.selected = true
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

func TestProjectionBatchPlanEnforcesStrictLogicalMutationByteCap(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 10, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1000, DecodedBytes: 1000, WriteBytes: 100, RecentAge: time.Hour, RecentBytes: 1000}
	s := apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1}, Phase: "source", Now: time.Now(), Documents: []apptypes.ProjectionDocument{{Sequence: 1, Text: "small", StoredBytes: 5, DecodedBytes: 5}}}
	_, err := usecase.PlanProjectionBatch(s, b)
	var oversized *apptypes.SearchProjectionOversizeError
	if !errors.As(err, &oversized) || oversized.Bytes <= b.WriteBytes {
		t.Fatalf("error = %T %v, want required-total oversize", err, err)
	}
	b.WriteBytes = 1000
	p, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Ledger.LogicalWriteBytes; got > b.WriteBytes {
		t.Fatalf("reserved bytes = %d > %d", got, b.WriteBytes)
	}
	if len(p.Writes) != 1 || p.Writes[0].LogicalBytes != 146 {
		t.Fatalf("logical formula = %+v, want one 146-byte mutation including fingerprints", p.Writes)
	}
}

func TestResumeStatusConsumesSameWallBudgetAndPreventsSelectionOrMutation(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, RecentBytes: 1}
	store := &wallBudgetStore{budget: b, delayStatus: true}
	_, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), b, time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if store.selected || store.applied {
		t.Fatalf("post-deadline calls: selected=%v applied=%v", store.selected, store.applied)
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

type multiBatchProjectionStore struct {
	budget     apptypes.SearchProjectionBudget
	checkpoint int64
	batches    int
}

func (s *multiBatchProjectionStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	return apptypes.SearchProjectionGeneration{}, nil
}
func (s *multiBatchProjectionStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: "rebuilding", Phase: "source", ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *multiBatchProjectionStore) SelectSnapshot(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.ProjectionSnapshot, error) {
	next := s.checkpoint + 1
	return apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", ConfigHash: s.budget.ConfigHash(), SourceRevision: 1, HighWater: 3, Checkpoint: s.checkpoint}, Phase: "source", Documents: []apptypes.ProjectionDocument{{Sequence: next, EventID: fmt.Sprintf("e%d", next), SessionID: "s", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Text: "body", StoredBytes: 4, DecodedBytes: 4}}, SourceDone: next == 3, Now: time.Now().UTC()}, nil
}
func (s *multiBatchProjectionStore) ApplyBatch(_ context.Context, p apptypes.ProjectionBatchPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	s.checkpoint = p.NextCheckpoint
	s.batches++
	return apptypes.SearchProjectionProgress{Selected: 1, Written: 1, Completed: p.Completed, GenerationID: "g"}, nil
}
func (s *multiBatchProjectionStore) CleanupBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error) {
	return apptypes.SearchProjectionProgress{}, nil
}
func (s *multiBatchProjectionStore) MarkFailed(context.Context, string, int64, string, time.Time) error {
	return nil
}

func TestSearchProjectionResumeUntilRunsDurableBoundedBatches(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, RecentBytes: 100}
	store := &multiBatchProjectionStore{budget: b}
	result, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Batches != 2 || result.StopReason != "max_batches" || store.checkpoint != 2 {
		t.Fatalf("result=%+v checkpoint=%d", result, store.checkpoint)
	}
}

func TestSearchProjectionResumeUntilContinuesFromDurableCheckpoint(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, RecentBytes: 100}
	store := &multiBatchProjectionStore{budget: b}
	u := usecase.NewSearchProjectionUsecase(store)
	first, err := u.ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 1, TotalWallTime: time.Minute}, time.Now())
	if err != nil || first.StopReason != "max_batches" || store.checkpoint != 1 {
		t.Fatalf("first=%+v checkpoint=%d error=%v", first, store.checkpoint, err)
	}
	second, err := u.ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 1, TotalWallTime: time.Minute}, time.Now())
	if err != nil || second.StopReason != "max_batches" || store.checkpoint != 2 {
		t.Fatalf("second=%+v checkpoint=%d error=%v", second, store.checkpoint, err)
	}
}

func TestSearchProjectionResumeUntilReportsTotalWallTimeWithoutLosingProgress(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, RecentBytes: 1}
	store := &wallBudgetStore{budget: b, delayStatus: true}
	result, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Millisecond}, time.Now())
	if err != nil || result.StopReason != "total_wall_time" || result.Batches != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestSearchProjectionResumeUntilPreservesParentCancellation(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, RecentBytes: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := usecase.NewSearchProjectionUsecase(&wallBudgetStore{budget: b, delayStatus: true}).ResumeUntil(ctx, b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want parent cancellation", err)
	}
}

func TestSearchProjectionResumeUntilPreservesPerBatchTimeout(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, RecentBytes: 1}
	_, err := usecase.NewSearchProjectionUsecase(&wallBudgetStore{budget: b, delayStatus: true}).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want per-batch timeout", err)
	}
}
