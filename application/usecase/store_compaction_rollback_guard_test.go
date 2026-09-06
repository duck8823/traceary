package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain"
)

type stubRollbackGuard struct {
	count int
	err   error
}

func (s stubRollbackGuard) CountRecordsAtRisk(context.Context, string, string) (int, error) {
	return s.count, s.err
}

func TestStoreCompactionRollbackRefusesPostSnapshotRecords(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	svc := NewStoreCompactionUsecase("/store", &faultJournal{run: run}, faultBuilder{}, faultFiles{}, faultLease{}, stubRollbackGuard{count: 3})
	_, err := svc.Rollback(context.Background(), run.ID)
	if err == nil {
		t.Fatal("Rollback succeeded with records at risk")
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "refuses compact rollback") {
		t.Fatalf("error = %q, want refusal naming the at-risk count", err)
	}
}

func TestStoreCompactionRollbackProceedsWithZeroAtRisk(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	ready := domain.CompactionObservation{Orientation: domain.OrientationRollbackReady, Source: run.SourceIdentity, Rollback: run.SourceIdentity, RollbackExists: true}
	svc := NewStoreCompactionUsecase("/store", &faultJournal{run: run}, faultBuilder{}, faultFiles{observation: ready}, faultLease{}, stubRollbackGuard{})
	got, err := svc.Rollback(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionRolledBack {
		t.Fatalf("phase = %q, want rolled back", got.Phase)
	}
}

func TestStoreCompactionRollbackFailsClosedWithoutGuard(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	svc := NewStoreCompactionUsecase("/store", &faultJournal{run: run}, faultBuilder{}, faultFiles{}, faultLease{})
	_, err := svc.Rollback(context.Background(), run.ID)
	if err == nil {
		t.Fatal("Rollback succeeded without a guard")
	}
	if !strings.Contains(err.Error(), "guard is not configured") {
		t.Fatalf("error = %q, want missing-guard refusal", err)
	}
}

func TestStoreCompactionRollbackSurfacesGuardError(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	svc := NewStoreCompactionUsecase("/store", &faultJournal{run: run}, faultBuilder{}, faultFiles{}, faultLease{}, stubRollbackGuard{err: errors.New("guard boom")})
	_, err := svc.Rollback(context.Background(), run.ID)
	if err == nil || !strings.Contains(err.Error(), "guard boom") {
		t.Fatalf("error = %v, want guard failure", err)
	}
}
