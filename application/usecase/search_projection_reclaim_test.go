package usecase_test

import (
	"context"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

type reclaimFakeStore struct {
	wallBudgetStore
	gens      []apptypes.SearchProjectionTerminalGeneration
	pages     int
	delayPage bool
}

func (s *reclaimFakeStore) ListTerminalGenerations(context.Context) ([]apptypes.SearchProjectionTerminalGeneration, error) {
	return s.gens, nil
}

func (s *reclaimFakeStore) ReclaimTerminalGenerationPage(ctx context.Context, _ string, _ apptypes.SearchProjectionBudget, _ int, _ time.Time) (apptypes.SearchProjectionReclaimProgress, error) {
	s.pages++
	if s.delayPage {
		<-ctx.Done()
		return apptypes.SearchProjectionReclaimProgress{}, ctx.Err() //nolint:wrapcheck // Test fake preserves context cancellation identity.
	}
	return apptypes.SearchProjectionReclaimProgress{Deleted: 1, Done: true}, nil
}

func TestReclaimTerminalGenerationsStopsOnWallTime(t *testing.T) {
	store := &reclaimFakeStore{
		gens:      []apptypes.SearchProjectionTerminalGeneration{{GenerationID: "g1", LifecycleState: "failed"}},
		delayPage: true,
	}
	u := usecase.NewSearchProjectionUsecase(store)
	got, err := u.ReclaimTerminalGenerations(context.Background(), apptypes.DefaultSearchProjectionBudget(), apptypes.SearchProjectionRunOptions{TotalWallTime: 20 * time.Millisecond}, time.Now())
	if err != nil {
		t.Fatalf("ReclaimTerminalGenerations() error = %v", err)
	}
	if got.StopReason != "wall_time" {
		t.Fatalf("StopReason=%q, want wall_time", got.StopReason)
	}
	if got.Complete {
		t.Fatal("Complete=true, want partial")
	}
}

func TestReclaimTerminalGenerationsReportsCompleteWhenNothingTerminal(t *testing.T) {
	u := usecase.NewSearchProjectionUsecase(&reclaimFakeStore{})
	got, err := u.ReclaimTerminalGenerations(context.Background(), apptypes.DefaultSearchProjectionBudget(), apptypes.SearchProjectionRunOptions{TotalWallTime: time.Minute}, time.Now())
	if err != nil {
		t.Fatalf("ReclaimTerminalGenerations() error = %v", err)
	}
	if !got.Complete || got.StopReason != "complete" {
		t.Fatalf("got complete=%v reason=%q", got.Complete, got.StopReason)
	}
}
