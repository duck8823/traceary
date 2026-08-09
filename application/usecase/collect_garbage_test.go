package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type garbageCollectorStub struct {
	receivedBefore       time.Time
	receivedFoldedBefore time.Time
	receivedDryRun       bool
	deletedCount         int
	err                  error
}

func (s *garbageCollectorStub) Initialize(_ context.Context) error { return nil }
func (s *garbageCollectorStub) CreateBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *garbageCollectorStub) RestoreBackup(_ context.Context, _ string, _ bool) error {
	return nil
}
func (s *garbageCollectorStub) CloseStaleSessions(_ context.Context, _ time.Duration, _ bool, _ []types.SessionID) (int, error) {
	return 0, nil
}
func (s *garbageCollectorStub) DedupeContentEvents(_ context.Context, _ apptypes.ContentEventDedupeParams) (apptypes.ContentEventDedupeResult, error) {
	return apptypes.ContentEventDedupeResult{}, nil
}
func (s *garbageCollectorStub) RestoreContentEventDedupeRun(_ context.Context, _ string) (apptypes.ContentEventDedupeRestoreResult, error) {
	return apptypes.ContentEventDedupeRestoreResult{}, nil
}

func (s *garbageCollectorStub) CollectGarbage(
	_ context.Context,
	before time.Time,
	foldedBefore time.Time,
	_ apptypes.GarbageCollectionTarget,
	dryRun bool,
) (int, error) {
	s.receivedBefore = before
	s.receivedFoldedBefore = foldedBefore
	s.receivedDryRun = dryRun
	return s.deletedCount, s.err
}

func TestStoreManagementUsecase_CollectGarbage(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	runStart := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)

	t.Run("runs garbage collection successfully", func(t *testing.T) {
		t.Parallel()

		stub := &garbageCollectorStub{deletedCount: 3}
		sut := usecase.NewStoreManagementUsecase(stub)

		got, err := sut.CollectGarbage(context.Background(), cutoff, runStart, apptypes.GarbageCollectionTargetEvents, true)
		if err != nil {
			t.Fatalf("CollectGarbage() error = %v", err)
		}
		if !stub.receivedBefore.Equal(cutoff) {
			t.Fatalf("received before = %v, want %v", stub.receivedBefore, cutoff)
		}
		if !stub.receivedFoldedBefore.Equal(runStart) {
			t.Fatalf("received foldedBefore = %v, want %v", stub.receivedFoldedBefore, runStart)
		}
		if !stub.receivedDryRun {
			t.Fatalf("received dryRun = false, want true")
		}
		if diff := cmp.Diff(3, got.DeletedCount()); diff != "" {
			t.Fatalf("DeletedCount mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns error when cutoff time is missing", func(t *testing.T) {
		t.Parallel()

		sut := usecase.NewStoreManagementUsecase(&garbageCollectorStub{})

		_, err := sut.CollectGarbage(context.Background(), time.Time{}, runStart, apptypes.GarbageCollectionTargetEvents, false)
		if err == nil {
			t.Fatalf("CollectGarbage() error = nil, want error")
		}
	})

	// foldedBefore decides which refinements count as pre-existing coverage, so
	// a missing one would silently widen what an apply may discard.
	t.Run("returns error when the run start is missing", func(t *testing.T) {
		t.Parallel()

		sut := usecase.NewStoreManagementUsecase(&garbageCollectorStub{})

		_, err := sut.CollectGarbage(context.Background(), cutoff, time.Time{}, apptypes.GarbageCollectionTargetEvents, false)
		if err == nil {
			t.Fatalf("CollectGarbage() error = nil, want error")
		}
	})
}

func (s *garbageCollectorStub) PurgeContentEventDedupeRun(_ context.Context, _ string) (apptypes.ContentEventDedupePurgeResult, error) {
	return apptypes.ContentEventDedupePurgeResult{}, nil
}

func (s *garbageCollectorStub) ListContentEventDedupeRuns(_ context.Context) ([]apptypes.ContentEventDedupeRun, error) {
	return nil, nil
}
