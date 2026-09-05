package usecase

import (
	"context"
	"errors"
	"strings"
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
	// allowRecheckForPublish opts into the pre-swap identity fence succeeding.
	// It is off by default so the after-exchange tests keep failing loudly if
	// the fence is ever applied to an already-swapped run.
	allowRecheckForPublish   bool
	rejectRetiredSearchIndex error
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
func (f *recoveryFiles) RejectRetiredSearchIndex(context.Context, domain.PreparedStoreUpgradeRun) error {
	return f.rejectRetiredSearchIndex
}
func (f *recoveryFiles) RecheckForPublish(context.Context, domain.PreparedStoreUpgradeRun) error {
	f.rechecked = true
	if f.allowRecheckForPublish {
		return nil
	}
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

func (recoveryLease) AcquireExclusive(context.Context, string) (func(), error) {
	return func() {}, nil
}
func (recoveryLease) HoldsExclusive(string) bool { return false }

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
	run := domain.PreparedStoreUpgradeRun{ID: "run", SourcePath: "/copy.db", Phase: domain.PreparedStoreUpgradeSwapIntent, Operation: domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade, ConsumerBinding: "binding", Candidate: identity, Budget: domain.PreparedStoreUpgradeBudget{PublishLockLimit: time.Second}}
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
	run := domain.PreparedStoreUpgradeRun{ID: "run", SourcePath: "/copy.db", Phase: domain.PreparedStoreUpgradeRollbackSwapIntent, Operation: domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade, ConsumerBinding: "binding", Budget: domain.PreparedStoreUpgradeBudget{PublishLockLimit: time.Second}}
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

// TestPreparedStoreUpgradeRefusesToPublishARetiredSearchIndex covers the
// sibling protocol's forward exchange. Only the payload-rehearsal migration
// recipe is registered today and that operation is exempt from the refusal, so
// this asserts the wiring rather than a live failure: Operation.Known() admits
// compaction, and a recipe registered for it must not inherit an unguarded
// publication path. Recovery is the harder case — it reaches the exchange
// through executeRecoveryAction, which handled both directions together until
// the forward one was split out.
func TestPreparedStoreUpgradeRefusesToPublishARetiredSearchIndex(t *testing.T) {
	identity := domain.StoreFileIdentity{Device: 1, Inode: 2, Mode: 0o600, Links: 1}
	run := domain.PreparedStoreUpgradeRun{ID: "run", SourcePath: "/copy.db", Phase: domain.PreparedStoreUpgradeSwapIntent, Operation: domain.PreparedStoreUpgradeOperationCompaction, ConsumerBinding: "binding", Candidate: identity, PreparedCandidateIdentity: identity, Budget: domain.PreparedStoreUpgradeBudget{PublishLockLimit: time.Second}}
	journal := &recoveryJournal{run: run}
	files := &recoveryFiles{
		observation:              domain.PreparedStoreUpgradeObservation{Orientation: domain.OrientationCandidateReady, Source: domain.StoreFileIdentity{Device: 1, Inode: 1}, Candidate: identity, CandidateExists: true},
		allowRecheckForPublish:   true,
		rejectRetiredSearchIndex: errors.New("legacy search index family is still present"),
	}
	service := NewPreparedStoreUpgradeUsecase(run.SourcePath, journal, files, recoveryLease{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{run.Operation: recoveryRecipe{}})

	_, err := service.Resume(context.Background(), run.ID)
	if err == nil {
		t.Fatal("Resume() error = nil, want a refusal to publish a candidate carrying the retired search family")
	}
	if !strings.Contains(err.Error(), "legacy search index family") {
		t.Fatalf("Resume() error = %v, want the retired-search-index refusal", err)
	}
	switch journal.run.Phase {
	case domain.PreparedStoreUpgradeSwapped, domain.PreparedStoreUpgradeCommitted:
		t.Fatalf("run reached %s; the candidate was published despite the refusal", journal.run.Phase)
	}
}
