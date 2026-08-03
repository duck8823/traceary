package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type recoveryJournal struct {
	run domain.PreparedStoreUpgradeRun
}

func (j *recoveryJournal) Create(context.Context, domain.PreparedStoreUpgradeRun) error { return nil }
func (j *recoveryJournal) Load(context.Context, string) (domain.PreparedStoreUpgradeRun, error) {
	return j.run, nil
}
func (j *recoveryJournal) FindActive(context.Context, domain.PreparedStoreUpgradeOperation, string, string) (domain.PreparedStoreUpgradeRun, error) {
	return j.run, nil
}
func (j *recoveryJournal) Append(_ context.Context, run domain.PreparedStoreUpgradeRun) error {
	j.run = run
	return nil
}

type recoveryFiles struct {
	observation domain.PreparedStoreUpgradeObservation
	rechecked   bool
}

func (f *recoveryFiles) Plan(context.Context, domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeRun, error) {
	return domain.PreparedStoreUpgradeRun{}, errors.New("unexpected plan")
}
func (f *recoveryFiles) PrepareCandidate(context.Context, domain.PreparedStoreUpgradeRun) (domain.StoreFileIdentity, error) {
	return domain.StoreFileIdentity{}, errors.New("unexpected prepare")
}
func (f *recoveryFiles) RemoveOwnedPartialCandidate(context.Context, domain.PreparedStoreUpgradeRun, domain.PreparedStoreUpgradeObservation) error {
	return errors.New("unexpected cleanup")
}
func (f *recoveryFiles) Recheck(context.Context, domain.PreparedStoreUpgradeRun) error { return nil }
func (f *recoveryFiles) RecheckForPublish(context.Context, domain.PreparedStoreUpgradeRun) error {
	f.rechecked = true
	return errors.New("pre-swap fence must not run after exchange")
}
func (f *recoveryFiles) FenceCandidate(_ context.Context, run domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeRun, error) {
	return run, nil
}
func (f *recoveryFiles) Exchange(context.Context, domain.PreparedStoreUpgradeRun) error { return nil }
func (f *recoveryFiles) PublishRollback(context.Context, domain.PreparedStoreUpgradeRun) error {
	f.observation.Orientation = domain.OrientationRollbackReady
	f.observation.CandidateExists = false
	return nil
}
func (f *recoveryFiles) Observe(context.Context, domain.PreparedStoreUpgradeRun) (domain.PreparedStoreUpgradeObservation, error) {
	return f.observation, nil
}
func (f *recoveryFiles) SyncRecoveredOrientation(context.Context, domain.PreparedStoreUpgradeRun, domain.PreparedStoreUpgradeObservation) error {
	return nil
}

type recoveryLease struct{}

func (recoveryLease) AcquireExclusive(context.Context, string) (func(), error) { return func() {}, nil }

type recoveryRecipe struct{}

func (recoveryRecipe) Plan(context.Context, application.PreparedCandidateRequest) (string, error) {
	return "", errors.New("unexpected plan")
}
func (recoveryRecipe) Build(context.Context, application.PreparedCandidateRequest) error { return nil }
func (recoveryRecipe) ClassifyOwnedCandidate(context.Context, application.PreparedCandidateInspection) (domain.CandidateCondition, error) {
	return domain.CandidateConditionUnknown, nil
}
func (recoveryRecipe) Sync(context.Context, application.PreparedCandidateRequest) error { return nil }
func (recoveryRecipe) Verify(context.Context, application.PreparedCandidateRequest) (domain.PreparedCandidateEvidence, error) {
	return domain.PreparedCandidateEvidence{}, nil
}

func TestPreparedStoreUpgradeResumeConvergesAfterExchangeBeforeSwappedRecord(t *testing.T) {
	identity := domain.StoreFileIdentity{Device: 1, Inode: 2, Mode: 0o600, Links: 1}
	run := domain.PreparedStoreUpgradeRun{ID: "run", SourcePath: "/copy.db", Phase: domain.PreparedStoreUpgradeSwapIntent, Operation: domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration, ConsumerBinding: "binding", Candidate: identity, Budget: domain.PreparedStoreUpgradeBudget{PublishLockLimit: time.Second}}
	journal := &recoveryJournal{run: run}
	files := &recoveryFiles{observation: domain.PreparedStoreUpgradeObservation{Orientation: domain.OrientationSwapped, Source: identity}}
	service := NewPreparedStoreUpgradeUsecase(run.SourcePath, journal, files, recoveryLease{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{run.Operation: recoveryRecipe{}})
	receipt, err := service.Resume(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.run.Phase != domain.PreparedStoreUpgradeCommitted || files.rechecked || receipt.RunID != run.ID {
		t.Fatalf("phase=%s rechecked=%v receipt=%+v", journal.run.Phase, files.rechecked, receipt)
	}
}

func TestPreparedStoreUpgradeRollbackCatchesUpAfterRollbackExchange(t *testing.T) {
	run := domain.PreparedStoreUpgradeRun{ID: "run", SourcePath: "/copy.db", Phase: domain.PreparedStoreUpgradeRollbackSwapIntent, Operation: domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration, ConsumerBinding: "binding", Budget: domain.PreparedStoreUpgradeBudget{PublishLockLimit: time.Second}}
	journal := &recoveryJournal{run: run}
	files := &recoveryFiles{observation: domain.PreparedStoreUpgradeObservation{Orientation: domain.OrientationRolledBack}}
	service := NewPreparedStoreUpgradeUsecase(run.SourcePath, journal, files, recoveryLease{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{run.Operation: recoveryRecipe{}})
	rolled, err := service.Rollback(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Phase != domain.PreparedStoreUpgradeRolledBack || journal.run.Phase != domain.PreparedStoreUpgradeRolledBack {
		t.Fatalf("rolled=%s journal=%s", rolled.Phase, journal.run.Phase)
	}
}
