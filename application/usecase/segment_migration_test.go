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
	action, ok := f.run.NextAction()
	if !ok {
		return f.run, domain.ErrSegmentMigrationTransition
	}
	return f.ExecuteSegmentMigrationAction(context.Background(), "run", action, migrationUsecaseBudget(16))
}
func (f *segmentMigrationRepositoryFake) ExecuteSegmentMigrationAction(_ context.Context, _ string, action domain.SegmentMigrationAction, _ types.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	want, ok := f.run.NextAction()
	if !ok || want != action {
		return f.run, domain.ErrSegmentMigrationTransition
	}
	var err error
	switch action {
	case domain.SegmentMigrationActionCopyPage:
		f.run, err = f.run.CheckpointPage(domain.SegmentMigrationPageProof{NextSequence: 2, Rows: 1, PlainBytes: 1})
	case domain.SegmentMigrationActionBuildCandidate:
		f.run, err = f.run.RecordCandidateBuilt(domain.SegmentMigrationCandidateProof{SourceDigest: f.run.PlanDigest, Basename: "segment", ManifestDigest: f.run.PlanDigest, FileDigest: f.run.PlanDigest})
	case domain.SegmentMigrationActionSeal:
		f.run, err = f.run.RecordCatalogPhase(domain.SegmentMigrationSealed, 1)
	case domain.SegmentMigrationActionVerify:
		f.run, err = f.run.RecordCatalogPhase(domain.SegmentMigrationVerifiedShadow, 2)
	default:
		phase := map[domain.SegmentMigrationAction]domain.SegmentMigrationPhase{domain.SegmentMigrationActionBeginCopy: domain.SegmentMigrationCopying, domain.SegmentMigrationActionRecordInstallIntent: domain.SegmentMigrationInstallIntent, domain.SegmentMigrationActionInstall: domain.SegmentMigrationInstalled, domain.SegmentMigrationActionRecordSealIntent: domain.SegmentMigrationSealIntent, domain.SegmentMigrationActionRecordVerifyIntent: domain.SegmentMigrationVerifyIntent}[action]
		f.run, err = f.run.Advance(phase)
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
func (f *segmentMigrationRepositoryFake) ExecuteSegmentMigrationRollbackAction(_ context.Context, _ string, action domain.SegmentMigrationRollbackAction, _ types.SegmentMigrationBudget) (domain.SegmentMigrationRun, error) {
	want, ok := f.run.NextRollbackAction()
	if !ok || want != action {
		return f.run, domain.ErrSegmentMigrationTransition
	}
	to := domain.SegmentMigrationRollbackIntent
	if action == domain.SegmentMigrationRollbackActionComplete {
		to = domain.SegmentMigrationRolledBack
	}
	var err error
	f.run, err = f.run.Advance(to)
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
	got, err := usecase.NewSegmentMigrationUsecase(fake).Resume(context.Background(), "run", migrationUsecaseBudget(16))
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
