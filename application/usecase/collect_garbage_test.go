package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/usecase"
)

type garbageCollectorStub struct {
	receivedPath   string
	receivedBefore time.Time
	receivedDryRun bool
	deletedCount   int
	err            error
}

func (s *garbageCollectorStub) CollectGarbage(
	_ context.Context,
	dbPath string,
	before time.Time,
	dryRun bool,
) (int, error) {
	s.receivedPath = dbPath
	s.receivedBefore = before
	s.receivedDryRun = dryRun
	return s.deletedCount, s.err
}

func TestCollectGarbageUsecase_Run(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)

	t.Run("gc を実行できる", func(t *testing.T) {
		t.Parallel()

		stub := &garbageCollectorStub{deletedCount: 3}
		sut := usecase.NewCollectGarbageUsecase(stub)

		got, err := sut.Run(context.Background(), usecase.CollectGarbageInput{
			DBPath: "/tmp/traceary.db",
			Before: cutoff,
			DryRun: true,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stub.receivedPath != "/tmp/traceary.db" {
			t.Fatalf("received path = %q, want %q", stub.receivedPath, "/tmp/traceary.db")
		}
		if !stub.receivedBefore.Equal(cutoff) {
			t.Fatalf("received before = %v, want %v", stub.receivedBefore, cutoff)
		}
		if !stub.receivedDryRun {
			t.Fatalf("received dryRun = false, want true")
		}
		if got.DeletedCount != 3 {
			t.Fatalf("DeletedCount = %d, want 3", got.DeletedCount)
		}
	})

	t.Run("returns error when cutoff time is missing", func(t *testing.T) {
		t.Parallel()

		sut := usecase.NewCollectGarbageUsecase(&garbageCollectorStub{})

		_, err := sut.Run(context.Background(), usecase.CollectGarbageInput{
			DBPath: "/tmp/traceary.db",
		})
		if err == nil {
			t.Fatalf("Run() error = nil, want error")
		}
	})
}
