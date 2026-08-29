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

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type wallBudgetStore struct {
	budget             apptypes.SearchProjectionBudget
	applied            bool
	delayStatus        bool
	delayControlStatus bool
	statusDelay        time.Duration
	openDelay          time.Duration
	delayApply         bool
	selected           bool
	controlReads       int
	measuredReads      int
	state              string
	resumeReady        bool
	completeCleanup    bool
	starts             int
}

func (s *wallBudgetStore) delayOpen(ctx context.Context) error {
	if s.openDelay <= 0 {
		return nil
	}
	openCtx := application.StoreOpenContext(ctx)
	timer := time.NewTimer(s.openDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-openCtx.Done():
		return xerrors.Errorf("store open: %w", openCtx.Err())
	}
}

func (s *wallBudgetStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	s.starts++
	s.state = "rebuilding"
	return apptypes.SearchProjectionGeneration{GenerationID: "generation"}, nil
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
		delay := s.statusDelay
		if delay == 0 {
			delay = 50 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return apptypes.SearchProjectionControlStatus{}, xerrors.Errorf("control status: %w", ctx.Err())
		}
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
	if err := s.delayOpen(ctx); err != nil {
		return apptypes.ProjectionSnapshot{}, err
	}
	if s.completeCleanup {
		return apptypes.ProjectionSnapshot{
			Generation:  apptypes.SearchProjectionGeneration{GenerationID: "generation"},
			Phase:       "cleanup",
			CleanupAll:  true,
			CleanupDone: true,
			Now:         time.Now(),
		}, nil
	}
	if s.resumeReady {
		return apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "generation"}, Phase: "source", Now: time.Now()}, nil
	}
	<-ctx.Done()
	return apptypes.ProjectionSnapshot{}, ctx.Err()
}
func (s *wallBudgetStore) ApplyBatch(ctx context.Context, plan apptypes.ProjectionBatchPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	if err := s.delayOpen(ctx); err != nil {
		return apptypes.SearchProjectionProgress{}, err
	}
	if s.delayApply {
		<-ctx.Done()
		return apptypes.SearchProjectionProgress{}, xerrors.Errorf("apply batch: %w", ctx.Err())
	}
	s.applied = true
	return apptypes.SearchProjectionProgress{Completed: plan.Completed, GenerationID: plan.GenerationID}, nil
}
func (s *wallBudgetStore) CleanupBatch(ctx context.Context, plan apptypes.ProjectionBatchPlan, _ time.Duration, _ time.Time) (apptypes.SearchProjectionProgress, error) {
	if err := s.delayOpen(ctx); err != nil {
		return apptypes.SearchProjectionProgress{}, err
	}
	if s.delayApply {
		<-ctx.Done()
		return apptypes.SearchProjectionProgress{}, xerrors.Errorf("cleanup batch: %w", ctx.Err())
	}
	s.applied = true
	if plan.Completed {
		s.state = "complete"
	}
	return apptypes.SearchProjectionProgress{Completed: plan.Completed, GenerationID: plan.GenerationID}, nil
}
func (*wallBudgetStore) MarkFailed(context.Context, string, int64, string, time.Time) error {
	return nil
}

func TestCompleteGenerationLeavesMatchingCompleteGeneration(t *testing.T) {
	t.Parallel()
	b := apptypes.DefaultSearchProjectionBudget()
	store := &wallBudgetStore{budget: b, state: "complete"}
	u := usecase.NewSearchProjectionUsecase(store)
	if err := u.CompleteGeneration(context.Background(), b, time.Now()); err != nil {
		t.Fatalf("CompleteGeneration() error = %v", err)
	}
	if store.starts != 0 {
		t.Fatalf("Start called %d times, want 0 for a matching complete generation", store.starts)
	}
	if store.applied {
		t.Fatal("Resume applied a batch on a matching complete generation")
	}
}

func TestProjectionBatchPlanEnforcesStrictLogicalMutationByteCap(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 10, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1000, DecodedBytes: 1000, WriteBytes: 110, RecentAge: time.Hour, IndexFamilyBytes: 1000}
	s := apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1}, Phase: "source", Now: time.Now(), Documents: []apptypes.ProjectionDocument{{Sequence: 1, Text: "small", StoredBytes: 5, DecodedBytes: 5}}}
	p, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Exclusions) != 1 || p.Exclusions[0].Class != "write_bytes" || p.NextCheckpoint != 1 {
		t.Fatalf("exclusions = %+v checkpoint=%d, want one write_bytes exclusion at checkpoint 1", p.Exclusions, p.NextCheckpoint)
	}
	b.WriteBytes = 1000
	p, err = usecase.PlanProjectionBatch(s, b)
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

func TestProjectionBatchPlanClassifiesAndSkipsRowsAtSourceBoundaries(t *testing.T) {
	t.Parallel()
	base := apptypes.SearchProjectionBudget{Rows: 10, WallTime: time.Second, LockTime: time.Second, StoredBytes: 10, DecodedBytes: 10, WriteBytes: 10_000, RecentAge: time.Hour, IndexFamilyBytes: 1000}
	tests := []struct {
		name                    string
		stored, decoded         int64
		wantClass               string
		wantMeasured, wantLimit int64
	}{
		{name: "stored boundary admitted", stored: 10, decoded: 5},
		{name: "stored one over excluded", stored: 11, decoded: 10, wantClass: "stored_bytes", wantMeasured: 11, wantLimit: 10},
		{name: "decoded boundary admitted", stored: 5, decoded: 10},
		{name: "decoded one over excluded", stored: 5, decoded: 11, wantClass: "decoded_bytes", wantMeasured: 11, wantLimit: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1}, Phase: "source", Now: time.Now(), Documents: []apptypes.ProjectionDocument{{Sequence: 1, EventID: "e", SessionID: "s", Text: "body", StoredBytes: tt.stored, DecodedBytes: tt.decoded, Disposition: apptypes.ProjectionDispositionAdmitted}}}
			p, err := usecase.PlanProjectionBatch(s, base)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.wantClass, func() string {
				if len(p.Exclusions) == 0 {
					return ""
				}
				return p.Exclusions[0].Class
			}()); diff != "" {
				t.Fatalf("class (-want +got):\n%s", diff)
			}
			if tt.wantClass != "" {
				got := p.Exclusions[0]
				if diff := cmp.Diff([]int64{tt.wantMeasured, tt.wantLimit}, []int64{got.MeasuredBytes, got.ByteLimit}); diff != "" {
					t.Fatalf("measurements (-want +got):\n%s", diff)
				}
			} else if len(p.Writes) != 1 {
				t.Fatalf("writes=%d, want 1", len(p.Writes))
			}
		})
	}
}

func TestProjectionBatchPlanExcludedRowDoesNotChainSummary(t *testing.T) {
	b := apptypes.SearchProjectionBudget{Rows: 10, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 10, WriteBytes: 10_000, RecentAge: time.Hour, IndexFamilyBytes: 1000}
	s := apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1}, Phase: "source", Now: time.Now(), Documents: []apptypes.ProjectionDocument{
		{Sequence: 1, EventID: "excluded", SessionID: "s", Text: strings.Repeat("x", 148), StoredBytes: 5, DecodedBytes: 11, Disposition: apptypes.ProjectionDispositionExcluded},
		{Sequence: 2, EventID: "small", SessionID: "s", Text: "tiny", StoredBytes: 5, DecodedBytes: 5, Disposition: apptypes.ProjectionDispositionAdmitted},
	}}
	p, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Exclusions) != 1 || len(p.Writes) != 1 {
		t.Fatalf("plan=%+v", p)
	}
	if strings.Contains(p.Writes[0].Summary, "xxx") {
		t.Fatalf("excluded text leaked into summary: %q", p.Writes[0].Summary)
	}
}

func TestDefaultSearchProjectionBudgetWallTimeIsOneSecond(t *testing.T) {
	t.Parallel()
	if got := apptypes.DefaultSearchProjectionBudget().WallTime; got != time.Second {
		t.Fatalf("DefaultSearchProjectionBudget().WallTime = %s, want 1s", got)
	}
}

func TestResumeMakesProgressWhenStoreOpenExceedsBatchWallTime(t *testing.T) {
	t.Parallel()
	b := apptypes.DefaultSearchProjectionBudget()
	b.WallTime = time.Millisecond
	store := &wallBudgetStore{budget: b, completeCleanup: true, openDelay: 40 * time.Millisecond}
	progress, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), b, time.Now())
	if err != nil {
		t.Fatalf("Resume() error = %v, want progress when store open exceeds WallTime", err)
	}
	if !store.selected {
		t.Fatal("SelectSnapshot was not reached")
	}
	if !store.applied {
		t.Fatal("CleanupBatch was not reached")
	}
	if !progress.Completed {
		t.Fatalf("progress.Completed = false, want forward progress to complete")
	}
}

func TestCompleteGenerationSucceedsWhenStoreOpenExceedsBatchWallTime(t *testing.T) {
	t.Parallel()
	b := apptypes.DefaultSearchProjectionBudget()
	b.WallTime = time.Millisecond
	store := &wallBudgetStore{budget: b, state: "abandoned", completeCleanup: true, openDelay: 40 * time.Millisecond}
	if err := usecase.NewSearchProjectionUsecase(store).CompleteGeneration(context.Background(), b, time.Now()); err != nil {
		t.Fatalf("CompleteGeneration() error = %v", err)
	}
	if store.state != "complete" {
		t.Fatalf("state = %q, want complete", store.state)
	}
}

func TestResumeInspectSucceedsWhenBatchWallTimeIsShorterThanPing(t *testing.T) {
	t.Parallel()
	b := apptypes.DefaultSearchProjectionBudget()
	b.WallTime = time.Millisecond
	store := &wallBudgetStore{budget: b, delayControlStatus: true, resumeReady: true}
	_, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), b, time.Now())
	if err != nil && strings.Contains(err.Error(), "inspect projection before resume") {
		t.Fatalf("Resume() inspect used batch WallTime: %v", err)
	}
	if store.controlReads < 1 {
		t.Fatal("SearchProjectionControlStatus was not called")
	}
	if !store.selected {
		t.Fatal("SelectSnapshot was not reached, want inspect to complete under parent context")
	}
}

func TestResumeInspectHonorsParentCancellation(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 1, WriteBytes: 1, RecentAge: time.Hour, IndexFamilyBytes: 1}
	store := &wallBudgetStore{budget: b, delayControlStatus: true, statusDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := usecase.NewSearchProjectionUsecase(store).Resume(ctx, b, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want parent cancellation", err)
	}
	if store.selected || store.applied {
		t.Fatalf("post-cancel calls: selected=%v applied=%v", store.selected, store.applied)
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
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Millisecond, LockTime: time.Second, StoredBytes: 1 << 20, DecodedBytes: 1 << 20, WriteBytes: 1 << 20, RecentAge: time.Hour, IndexFamilyBytes: 1 << 20}
	store := &wallBudgetStore{budget: b, completeCleanup: true, delayApply: true}
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
	delayAll     bool
	delayAttempt int
	attempts     int
	attemptDelay time.Duration
	// controlState and controlCheckpoint describe a persisted generation that
	// catch-up is about to replace; empty and zero keep the resume-in-place
	// shape the other tests rely on.
	controlState      string
	controlCheckpoint int64
	// checkpointOnFailure stands in for another opener committing a batch
	// while this one was failing; zero leaves the checkpoint where it was.
	checkpointOnFailure int64
	rowWorkCap          bool
}

func (s *adaptiveProjectionStore) Start(context.Context, apptypes.SearchProjectionBudget, time.Time) (apptypes.SearchProjectionGeneration, error) {
	// Starting a generation moves the row to rebuilding and resets its
	// checkpoint, which is what the resume that follows reads back.
	s.controlState = "rebuilding"
	s.controlCheckpoint = 0
	return apptypes.SearchProjectionGeneration{}, nil
}
func (s *adaptiveProjectionStore) SearchProjectionStatus(context.Context) (apptypes.SearchProjectionStatus, error) {
	return apptypes.SearchProjectionStatus{State: "rebuilding", Phase: "source", ConfigHash: s.budget.ConfigHash()}, nil
}
func (s *adaptiveProjectionStore) SearchProjectionControlStatus(context.Context) (apptypes.SearchProjectionControlStatus, error) {
	state := s.controlState
	if state == "" {
		state = "rebuilding"
	}
	return apptypes.SearchProjectionControlStatus{State: state, Phase: "source", Checkpoint: s.controlCheckpoint, ConfigHash: s.budget.ConfigHash(), CapacitySemanticsVersion: apptypes.SearchProjectionCapacitySemanticsVersion}, nil
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
	s.attempts++
	if s.delayAll || s.attempts == s.delayAttempt {
		timer := time.NewTimer(s.attemptDelay)
		defer timer.Stop()
		<-timer.C
	}
	if len(p.Exclusions) > 0 && len(p.Writes) == 0 {
		s.checkpoints = append(s.checkpoints, p.NextCheckpoint)
		return apptypes.SearchProjectionProgress{Selected: len(p.Exclusions), GenerationID: "g"}, nil
	}
	if s.rowWorkCap {
		if len(p.Writes) == 1 {
			w := p.Writes[0]
			return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{
				Code:   apptypes.SearchProjectionNoProgressRowWorkCap,
				Reason: "projection row work exceeded hold budget",
				Exclusion: apptypes.ProjectionExclusion{
					Sequence: w.Document.Sequence, EventID: w.Document.EventID, Class: "row_work",
					MeasuredBytes: 1, ByteLimit: 1,
				},
			}
		}
		return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{
			Code:   apptypes.SearchProjectionNoProgressRowWorkCap,
			Reason: "projection row work exceeded hold budget",
		}
	}
	if len(p.Writes) > s.maxRows || s.alwaysTooBig {
		if s.checkpointOnFailure != 0 {
			s.controlCheckpoint = s.checkpointOnFailure
		}
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
		// Stored/decoded/write look like batch caps but exclude oversize rows,
		// so they are capacity identity (#1754). Mutating the hash to drop
		// them makes these cases match and this test red.
		{name: "stored bytes", mutate: func(b *apptypes.SearchProjectionBudget) { b.StoredBytes = 101 }, match: false},
		{name: "write bytes", mutate: func(b *apptypes.SearchProjectionBudget) { b.WriteBytes = 301 }, match: false},
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

func TestSearchProjectionResumeRejectsChangedStoredBytes(t *testing.T) {
	t.Parallel()
	base := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 100, RecentAge: time.Hour, IndexFamilyBytes: 100}
	changed := base
	changed.StoredBytes++
	store := &wallBudgetStore{budget: base}
	_, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), changed, time.Now())
	if diff := cmp.Diff("search projection made no progress: budget does not match generation configuration", err.Error()); diff != "" {
		t.Fatalf("error (-want +got):\n%s", diff)
	}
}

func TestPlanProjectionBatchFailsWhenOnlyTheExclusionCannotFit(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 1, DecodedBytes: 100, WriteBytes: 120, RecentAge: time.Hour, IndexFamilyBytes: 100}
	s := apptypes.ProjectionSnapshot{
		Generation: apptypes.SearchProjectionGeneration{GenerationID: "generation"},
		Phase:      "source",
		Documents:  []apptypes.ProjectionDocument{{Sequence: 1, EventID: "event", StoredBytes: 2, DecodedBytes: 2}},
	}
	_, err := usecase.PlanProjectionBatch(s, b)
	var oversized *apptypes.SearchProjectionOversizeError
	if !errors.As(err, &oversized) {
		t.Fatalf("error=%T %v, want SearchProjectionOversizeError", err, err)
	}
	if diff := cmp.Diff("write_bytes", oversized.Class); diff != "" {
		t.Fatalf("class (-want +got):\n%s", diff)
	}
	if oversized.Bytes <= oversized.Limit {
		t.Fatalf("error bytes=%d limit=%d, want required total above limit", oversized.Bytes, oversized.Limit)
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

func TestSearchProjectionCatchUpShrinksFailedBatch(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: 250 * time.Millisecond, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, maxRows: 2}
	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(context.Background(), b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]int{4, 2}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, result.Batches); diff != "" {
		t.Fatalf("catch-up batches (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, result.Selected); diff != "" {
		t.Fatalf("catch-up selected rows (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionCatchUpBoundsAdaptiveAttempts(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 256, WallTime: 50 * time.Millisecond, LockTime: time.Millisecond, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, alwaysTooBig: true, delayAll: true, attemptDelay: 10 * time.Millisecond}
	_, err := usecase.NewSearchProjectionUsecase(store).CatchUp(context.Background(), b, time.Now())
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) {
		t.Fatalf("error=%v, want no-progress error", err)
	}
	if diff := cmp.Diff(apptypes.SearchProjectionNoProgressSingleRowLockDurationCap, noProgress.Code); diff != "" {
		t.Fatalf("error code (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{256, 128, 64, 32, 16, 8, 4, 2, 1}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
	}
}

// The catch-up log names this checkpoint as the row the generation cannot get
// past, so it has to belong to the generation that stalled. Catch-up may replace
// the generation before it resumes, and the replacement starts from zero.
func TestSearchProjectionCatchUpReportsTheStalledGenerationsCheckpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		controlState        string
		checkpointOnFailure int64
		want                int64
	}{
		{
			name:         "resuming in place reports the persisted checkpoint",
			controlState: "rebuilding",
			want:         1842,
		},
		{
			name:         "replacing the generation reports the new one, not the replaced one",
			controlState: "drifted",
			want:         0,
		},
		{
			name:                "a concurrent opener's committed batch is reported, not the entry read",
			controlState:        "rebuilding",
			checkpointOnFailure: 1900,
			want:                1900,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: 250 * time.Millisecond, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
			store := &adaptiveProjectionStore{budget: b, alwaysTooBig: true, controlState: test.controlState, controlCheckpoint: 1842, checkpointOnFailure: test.checkpointOnFailure}
			result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(context.Background(), b, time.Now())
			var noProgress *apptypes.SearchProjectionNoProgressError
			if !errors.As(err, &noProgress) || noProgress.Code != apptypes.SearchProjectionNoProgressSingleRowLockDurationCap {
				t.Fatalf("error=%v, want single-row lock duration cap", err)
			}
			if diff := cmp.Diff(test.want, result.Checkpoint); diff != "" {
				t.Fatalf("reported checkpoint (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSearchProjectionCatchUpReportsWallBoundExhaustionWithoutBatch(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 256, WallTime: 50 * time.Millisecond, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, alwaysTooBig: true, delayAttempt: 4, attemptDelay: time.Second}
	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(context.Background(), b, time.Now())
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) {
		t.Fatalf("error=%v, want no-progress error", err)
	}
	if diff := cmp.Diff("catch-up total wall time bound exhausted before a batch was committed", noProgress.Reason); diff != "" {
		t.Fatalf("error reason (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, result.Batches); diff != "" {
		t.Fatalf("catch-up batches (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(4, len(store.plannedRows)); diff != "" {
		t.Fatalf("attempts before wall bound (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionResumeDoesNotShrinkSingleBatch(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, maxRows: 0, alwaysTooBig: true}
	_, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), b, time.Now())
	var noProgress *apptypes.SearchProjectionNoProgressError
	if !errors.As(err, &noProgress) {
		t.Fatalf("error=%v, want no-progress error", err)
	}
	if diff := cmp.Diff(apptypes.SearchProjectionNoProgressLockDurationCap, noProgress.Code); diff != "" {
		t.Fatalf("error code (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{4}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
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

func TestSearchProjectionResumeExcludesOneWriteWhenBudgetRowsRemain(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, rowWorkCap: true, checkpoints: []int64{2}}
	progress, err := usecase.NewSearchProjectionUsecase(store).Resume(context.Background(), b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]int{4}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int64{2, 3}, store.checkpoints); diff != "" {
		t.Fatalf("checkpoints (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, progress.Selected); diff != "" {
		t.Fatalf("selected (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionResumeUntilExcludesSingleRowWorkCap(t *testing.T) {
	t.Parallel()
	b := apptypes.SearchProjectionBudget{Rows: 4, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	store := &adaptiveProjectionStore{budget: b, rowWorkCap: true}
	result, err := usecase.NewSearchProjectionUsecase(store).ResumeUntil(context.Background(), b, apptypes.SearchProjectionRunOptions{MaxBatches: 1, TotalWallTime: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]int{4, 2, 1}, store.plannedRows); diff != "" {
		t.Fatalf("planned rows (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int64{1}, store.checkpoints); diff != "" {
		t.Fatalf("exclusion checkpoint (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, result.Progress.Selected); diff != "" {
		t.Fatalf("selected (-want +got):\n%s", diff)
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

func TestPlanProjectionBatchEvictsOneRowWithoutCheckpoint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 100, RecentAge: time.Hour, IndexFamilyBytes: 100}
	s := apptypes.ProjectionSnapshot{
		Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1, Checkpoint: 7},
		Phase:      "source", Now: now, RecentSourceBytes: 12, RecentSourceCeilingBytes: 10,
		Cleanup: []apptypes.ProjectionCleanupCandidate{{Class: "eviction", RowID: 3, LogicalBytes: 4, ReleasedSourceBytes: 5, CreatedAtNorm: now.Format(time.RFC3339Nano), Expired: true}},
	}
	got, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []apptypes.ProjectionCleanupCandidate{s.Cleanup[0]}
	if diff := cmp.Diff(want, got.Cleanup); diff != "" {
		t.Fatalf("cleanup (-want +got):\n%s", diff)
	}
	if got.NextCheckpoint != s.Generation.Checkpoint || got.Ledger.Rows != 1 {
		t.Fatalf("plan=%+v, want one eviction without checkpoint movement", got)
	}
}

func TestPlanProjectionBatchAdmitsAfterUnparseableCandidateIsNotExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	b := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	s := apptypes.ProjectionSnapshot{
		Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", SourceRevision: 1, Checkpoint: 7},
		Phase:      "source", Now: now, RecentSourceBytes: 1, RecentSourceCeilingBytes: 100,
		Cleanup:   []apptypes.ProjectionCleanupCandidate{{Class: "eviction", RowID: 3, LogicalBytes: 4, ReleasedSourceBytes: 5, CreatedAtNorm: "not-a-timestamp"}},
		Documents: []apptypes.ProjectionDocument{{Sequence: 8, EventID: "e", SessionID: "s", CreatedAt: "not-a-timestamp", Text: "body", StoredBytes: 4, DecodedBytes: 4}},
	}
	got, err := usecase.PlanProjectionBatch(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(int64(8), got.NextCheckpoint); diff != "" {
		t.Fatalf("checkpoint (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, len(got.Writes)); diff != "" {
		t.Fatalf("writes (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(0, len(got.Cleanup)); diff != "" {
		t.Fatalf("cleanup (-want +got):\n%s", diff)
	}
}

func TestPlanProjectionBatchCapacityBoundaries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	base := apptypes.SearchProjectionBudget{Rows: 1, WallTime: time.Second, LockTime: time.Second, StoredBytes: 100, DecodedBytes: 100, WriteBytes: 1000, RecentAge: time.Hour, IndexFamilyBytes: 100}
	tests := []struct {
		name           string
		snapshot       apptypes.ProjectionSnapshot
		wantCleanup    int
		wantNextPhase  string
		wantCheckpoint int64
		wantWrites     int
	}{
		{
			name:          "eviction under ceiling does not delegate to retention",
			snapshot:      apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g"}, Phase: "eviction", Now: now, RecentSourceBytes: 1, RecentSourceCeilingBytes: 100, CleanupDone: true, Cleanup: []apptypes.ProjectionCleanupCandidate{{Class: "eviction", RowID: 1, ReleasedSourceBytes: 1, LogicalBytes: 1, Expired: false}}},
			wantNextPhase: "cleanup",
		},
		{
			name:        "expired row is evicted",
			snapshot:    apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g"}, Phase: "eviction", Now: now, RecentSourceBytes: 1, RecentSourceCeilingBytes: 100, CleanupDone: true, Cleanup: []apptypes.ProjectionCleanupCandidate{{Class: "eviction", RowID: 1, ReleasedSourceBytes: 1, LogicalBytes: 1, Expired: true}}},
			wantCleanup: 1, wantNextPhase: "cleanup",
		},
		{
			name:           "rows one advances one checkpoint",
			snapshot:       apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g", Checkpoint: 4}, Phase: "source", Now: now, Documents: []apptypes.ProjectionDocument{{Sequence: 5, EventID: "e", SessionID: "s", CreatedAt: now.Format(time.RFC3339Nano), Text: "body", StoredBytes: 4, DecodedBytes: 4}}},
			wantCheckpoint: 5, wantWrites: 1,
		},
		{
			name:           "zero ceiling admits no source rows",
			snapshot:       apptypes.ProjectionSnapshot{Generation: apptypes.SearchProjectionGeneration{GenerationID: "g"}, Phase: "source", Now: now, RecentSourceCeilingBytes: 0, RecentCutoffNorm: "9999-12-31T23:59:59.999999999Z", Documents: []apptypes.ProjectionDocument{{Sequence: 1, EventID: "e", SessionID: "s", CreatedAt: now.Format(time.RFC3339Nano), Text: "body", StoredBytes: 4, DecodedBytes: 4}}},
			wantCheckpoint: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := usecase.PlanProjectionBatch(tt.snapshot, base)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.wantCleanup, len(got.Cleanup)); diff != "" {
				t.Errorf("cleanup (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantNextPhase, got.NextPhase); diff != "" {
				t.Errorf("next phase (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCheckpoint, got.NextCheckpoint); diff != "" {
				t.Errorf("checkpoint (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWrites, len(got.Writes)); diff != "" {
				t.Errorf("writes (-want +got):\n%s", diff)
			}
		})
	}
}
