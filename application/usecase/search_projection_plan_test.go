package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type wallBudgetStore struct {
	budget             apptypes.SearchProjectionBudget
	applied            bool
	delayStatus        bool
	delayControlStatus bool
	selected           bool
	controlReads       int
	measuredReads      int
	state              string
	resumeReady        bool
}

func (s *wallBudgetStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	return apptypes.SearchProjectionGeneration{}, nil
}

//nolint:wrapcheck // Test fake preserves context cancellation identity.
func (s *wallBudgetStore) SearchProjectionStatus(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	s.measuredReads++
	if s.delayStatus {
		<-ctx.Done()
		return apptypes.SearchProjectionStatus{}, ctx.Err()
	}
	return apptypes.SearchProjectionStatus{State: "rebuilding", ConfigHash: s.budget.ConfigHash()}, nil
}

func (s *wallBudgetStore) SearchProjectionControlStatus(ctx context.Context) (apptypes.SearchProjectionControlStatus, error) {
	s.controlReads++
	if s.delayControlStatus {
		<-ctx.Done()
		return apptypes.SearchProjectionControlStatus{}, xerrors.Errorf("control status: %w", ctx.Err())
	}
	state := s.state
	if state == "" {
		state = "rebuilding"
	}
	return apptypes.SearchProjectionControlStatus{State: state, ConfigHash: s.budget.ConfigHash(), CapacitySemanticsVersion: apptypes.SearchProjectionCapacitySemanticsVersion}, nil
}

//nolint:wrapcheck // Test fake preserves context cancellation identity.
func (s *wallBudgetStore) SelectSnapshot(ctx context.Context, _ apptypes.SearchProjectionBudget, _ time.Time) (apptypes.ProjectionSnapshot, error) {
	s.selected = true
	if s.resumeReady {
		return apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "generation"}, Phase: "source", Now: time.Now()}, nil
	}
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
	b := apptypes.SearchProjectionBudget{Rows: 10, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1000, DecodedBytes: 1000, WriteBytes: 100, RecentAge: time.Hour, IndexFamilyBytes: 1000}
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1}
	store := &wallBudgetStore{budget: b, delayControlStatus: true}
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1}
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
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

type adaptiveProjectionStore struct {
	budget       apptypes.SearchProjectionBudget
	maxRows      int
	checkpoints  []int64
	plannedRows  []int
	alwaysTooBig bool
}

func (s *adaptiveProjectionStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	return apptypes.SearchProjectionGeneration{}, nil
}
func (s *adaptiveProjectionStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: "rebuilding", Phase: "source", ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *adaptiveProjectionStore) SearchProjectionControlStatus(context.Context) (apptypes.SearchProjectionControlStatus, error) {
	return apptypes.SearchProjectionControlStatus{State: "rebuilding", Phase: "source", ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *adaptiveProjectionStore) SelectSnapshot(_ context.Context, b apptypes.SearchProjectionBudget, _ time.Time) (apptypes.ProjectionSnapshot, error) {
	s.plannedRows = append(s.plannedRows, b.Rows)
	documents := make([]apptypes.ProjectionDocument, 0, b.Rows)
	for sequence := s.checkpoint() + 1; sequence <= 3 && len(documents) < b.Rows; sequence++ {
		documents = append(documents, apptypes.ProjectionDocument{Sequence: sequence, EventID: fmt.Sprintf("e%d", sequence), SessionID: "s", CreatedAt: "2026-08-10T00:00:00Z", Text: "body", StoredBytes: 4, DecodedBytes: 4})
	}
	return apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", ConfigHash: s.budget.ConfigHash(), SourceRevision: 1, HighWater: 3, Checkpoint: s.checkpoint()}, Phase: "source", Documents: documents, SourceDone: len(documents) > 0 && documents[len(documents)-1].Sequence == 3, Now: time.Now().UTC()}, nil
}
func (s *adaptiveProjectionStore) ApplyBatch(_ context.Context, p apptypes.ProjectionBatchPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	if len(p.Writes) > s.maxRows || s.alwaysTooBig {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock duration cap exceeded"}
	}
	s.checkpoints = append(s.checkpoints, p.NextCheckpoint)
	return apptypes.SearchProjectionProgress{Selected: len(p.Writes), Written: len(p.Writes), GenerationID: "g"}, nil
}
func (s *adaptiveProjectionStore) CleanupBatch(context.Context, apptypes.ProjectionBatchPlan, time.Duration, time.Time) (apptypes.SearchProjectionProgress, error) {
	return apptypes.SearchProjectionProgress{}, nil
}
func (s *adaptiveProjectionStore) MarkFailed(context.Context, string, int64, string, time.Time) error {
	return nil
}
func (s *adaptiveProjectionStore) checkpoint() int64 {
	if len(s.checkpoints) == 0 {
		return 0
	}
	return s.checkpoints[len(s.checkpoints)-1]
}

type adaptiveInventoryProjectionStore struct {
	adaptiveProjectionStore
	cursor               int
	plannedInventoryRows []int
}

func (s *adaptiveInventoryProjectionStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: "rebuilding", Phase: "inventory", ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *adaptiveInventoryProjectionStore) SearchProjectionControlStatus(context.Context) (apptypes.SearchProjectionControlStatus, error) {
	return apptypes.SearchProjectionControlStatus{State: "rebuilding", Phase: "inventory", ConfigHash: s.budget.ConfigHash()}, nil
}

func (s *adaptiveInventoryProjectionStore) SelectInventory(_ context.Context, b apptypes.SearchProjectionBudget) (apptypes.SearchProjectionInventorySnapshot, error) {
	s.plannedInventoryRows = append(s.plannedInventoryRows, b.Rows)
	items := make([]apptypes.SearchProjectionInventoryItem, 0, b.Rows)
	for id := s.cursor + 1; id <= 3 && len(items) < b.Rows; id++ {
		items = append(items, apptypes.SearchProjectionInventoryItem{EventID: fmt.Sprintf("e%d", id), LogicalBytes: 1, Missing: true})
	}
	return apptypes.SearchProjectionInventorySnapshot{
		Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", ConfigHash: s.budget.ConfigHash(), SourceRevision: 1},
		Cursor:     fmt.Sprintf("e%d", s.cursor), CursorStarted: s.cursor > 0,
		Items: items, Done: s.cursor+len(items) == 3,
	}, nil
}

func (s *adaptiveInventoryProjectionStore) ApplyInventoryBatch(_ context.Context, p apptypes.SearchProjectionInventoryPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	if p.Ledger.Rows > s.maxRows {
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "inventory lock duration cap exceeded"}
	}
	s.cursor += p.Ledger.Rows
	return apptypes.SearchProjectionProgress{Selected: p.Ledger.Rows, Written: p.Ledger.Rows, Completed: p.Done, GenerationID: p.GenerationID}, nil
}

func TestSearchProjectionConfigHashSeparatesSemanticAndThroughputBudgets(t *testing.T) {
	t.Parallel()
	base := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 200, WriteBytes: 300, RecentAge: time.Hour, IndexFamilyBytes: 400}
	tests := []struct {
		name   string
		mutate func(*apptypes.SearchProjectionBudget)
		match  bool
	}{
		{name: "rows", mutate: func(b *apptypes.SearchProjectionBudget) { b.Rows = 1 }, match: true},
		{name: "lock time", mutate: func(b *apptypes.SearchProjectionBudget) { b.LockTime = 2 * time.Second }, match: true},
		{name: "wall time", mutate: func(b *apptypes.SearchProjectionBudget) { b.WallTime = 2 * time.Second }, match: true},
		{name: "stored bytes", mutate: func(b *apptypes.SearchProjectionBudget) { b.StoredBytes = 101 }, match: true},
		{name: "write bytes", mutate: func(b *apptypes.SearchProjectionBudget) { b.WriteBytes = 301 }, match: true},
		{name: "recent age", mutate: func(b *apptypes.SearchProjectionBudget) { b.RecentAge = 2 * time.Hour }, match: false},
		{name: "index family bytes", mutate: func(b *apptypes.SearchProjectionBudget) { b.IndexFamilyBytes = 401 }, match: false},
		{name: "decoded bytes", mutate: func(b *apptypes.SearchProjectionBudget) { b.DecodedBytes = 201 }, match: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base
			tt.mutate(&got)
			if diff := cmp.Diff(tt.match, base.ConfigHash() == got.ConfigHash()); diff != "" {
				t.Fatalf("hash match (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSearchProjectionResumeUntilShrinksOnlyTheFailedBatch(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, maxRows: 2}
	result, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(2, result.Batches); diff != "" {
		t.Fatalf("successful batches (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{4, 2, 4}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int64{2, 3}, store.checkpoints); diff != "" {
		t.Fatalf("checkpoints (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionResumeUntilShrinksInventoryBatch(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveInventoryProjectionStore{adaptiveProjectionStore: adaptiveProjectionStore{budget: b, maxRows: 2}}
	result, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(2, result.Batches); diff != "" {
		t.Fatalf("successful batches (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{4, 2, 4}, store.plannedInventoryRows); diff != "" {
		t.Fatalf("planned inventory rows (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionResumeUntilReportsSingleRowLockCap(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, maxRows: 0, alwaysTooBig: true}
	_, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 1, TotalWallTime: time.Minute}, time.Now())
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) {
		t.Fatalf("error=%v, want no-progress error", err)
	}
	if diff := cmp.Diff(apptypes.SearchProjectionNoProgressSingleRowLockDurationCap, noProgress.Code); diff != "" {
		t.Fatalf("error code (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{4, 2, 1}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
	}
}

func (s *multiBatchProjectionStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	return apptypes.SearchProjectionGeneration{}, nil
}
func (s *multiBatchProjectionStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: "rebuilding", Phase: "source", ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *multiBatchProjectionStore) SearchProjectionControlStatus(context.Context) (apptypes.SearchProjectionControlStatus, error) {
	return apptypes.SearchProjectionControlStatus{State: "rebuilding", Phase: "source", ConfigHash: s.budget.ConfigHash()}, nil
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1}
	store := &wallBudgetStore{budget: b, delayStatus: true}
	result, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Millisecond}, time.Now())
	if err != nil || result.StopReason != "total_wall_time" || result.Batches != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestSearchProjectionResumeUntilPreservesParentCancellation(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := usecase.NewSearchProjectionUsecase(&wallBudgetStore{budget: b, delayStatus: true}).ResumeUntil(ctx, b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want parent cancellation", err)
	}
}

func TestSearchProjectionResumeUntilPreservesPerBatchTimeout(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1}
	_, err := usecase.NewSearchProjectionUsecase(&wallBudgetStore{budget: b, delayStatus: true}).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 2, TotalWallTime: time.Minute}, time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want per-batch timeout", err)
	}
}
