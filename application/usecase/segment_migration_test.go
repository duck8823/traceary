//nolint:wrapcheck // The fake intentionally preserves domain transition errors.
package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
)

type segmentMigrationRepositoryFake struct{ run domain.SegmentMigrationRun }

func (f *segmentMigrationRepositoryFake) StartSegmentMigration(context.Context, types.SegmentMigrationStart) (domain.SegmentMigrationRun, error) {
	return f.run, nil
}
func (f *segmentMigrationRepositoryFake) LoadSegmentMigration(context.Context, string) (domain.SegmentMigrationRun, error) {
	return f.run, nil
}
func (f *segmentMigrationRepositoryFake) AdvanceSegmentMigration(_ context.Context, _ string, _ types.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	next := map[domain.SegmentMigrationPhase]domain.SegmentMigrationPhase{domain.SegmentMigrationPlanned: domain.SegmentMigrationCopying, domain.SegmentMigrationCopying: domain.SegmentMigrationCandidateBuilt, domain.SegmentMigrationCandidateBuilt: domain.SegmentMigrationInstallIntent, domain.SegmentMigrationInstallIntent: domain.SegmentMigrationInstalled, domain.SegmentMigrationInstalled: domain.SegmentMigrationSealIntent, domain.SegmentMigrationSealIntent: domain.SegmentMigrationSealed, domain.SegmentMigrationSealed: domain.SegmentMigrationVerifyIntent, domain.SegmentMigrationVerifyIntent: domain.SegmentMigrationVerifiedShadow}[f.run.Phase]
	var err error
	f.run, err = f.run.Advance(next)
	if next == domain.SegmentMigrationCandidateBuilt {
		f.run.NextSequence = 2
		f.run.CopiedRows = 1
		f.run.SourceDigest = f.run.PlanDigest
		f.run.CandidateBasename = "segment"
		f.run.SegmentID = "segment"
		f.run.ManifestDigest = f.run.PlanDigest
		f.run.FileDigest = f.run.PlanDigest
	}
	if next == domain.SegmentMigrationSealed {
		f.run.CatalogEpoch = 1
	}
	return f.run, err
}
func (f *segmentMigrationRepositoryFake) AdvanceSegmentMigrationRollback(_ context.Context, _ string, _ types.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	next := domain.SegmentMigrationRollbackIntent
	if f.run.Phase == next {
		next = domain.SegmentMigrationRolledBack
	}
	var err error
	f.run, err = f.run.Advance(next)
	return f.run, err
}
func (f *segmentMigrationRepositoryFake) RecoverSegmentMigration(context.Context, string, types.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	return f.run, nil
}

func migrationUsecaseRun() domain.SegmentMigrationRun {
	return domain.SegmentMigrationRun{ID: "run", StoreID: "0123456789abcdef0123456789abcdef", ReservationID: "reservation", PlanDigest: "0000000000000000000000000000000000000000000000000000000000000000", Range: domain.CatalogRange{Start: 1, End: 1}, Phase: domain.SegmentMigrationPlanned, Revision: 1, NextSequence: 1}
}
func migrationUsecaseBudget(steps int) types.SegmentMigrationBudget {
	return types.SegmentMigrationBudget{PageRows: 1, MaxSteps: steps, WallTime: time.Second, LockTime: time.Second, MaxPlainBytes: 1, MaxStoredBytes: 1, MaxValuePlainBytes: 1, MaxValueStoredBytes: 1, MaxFileBytes: 1, MaxSummaryBytes: 1, MaxSummaryRows: 1, MaxWALBytes: 1, MinFreeDiskBytes: 1}
}

func TestSegmentMigrationUsecaseResumesToVerifiedShadow(t *testing.T) {
	fake := &segmentMigrationRepositoryFake{run: migrationUsecaseRun()}
	got, err := usecase.NewSegmentMigrationUsecase(fake).Resume(context.Background(), "run", migrationUsecaseBudget(8))
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.SegmentMigrationVerifiedShadow {
		t.Fatal(got.Phase)
	}
}
func TestSegmentMigrationUsecaseReturnsIncompleteAtStepCap(t *testing.T) {
	fake := &segmentMigrationRepositoryFake{run: migrationUsecaseRun()}
	_, err := usecase.NewSegmentMigrationUsecase(fake).Resume(context.Background(), "run", migrationUsecaseBudget(1))
	if !errors.Is(err, types.ErrSegmentMigrationIncomplete) {
		t.Fatalf("error=%v", err)
	}
}
