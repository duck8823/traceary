package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain"
)

type faultJournal struct {
	run             domain.CompactionRun
	failLoad        bool
	failAppendPhase domain.CompactionPhase
}

func (j *faultJournal) Create(context.Context, domain.CompactionRun) error { return nil }
func (j *faultJournal) Load(context.Context, string) (domain.CompactionRun, error) {
	if j.failLoad {
		return j.run, errors.New("load fault")
	}
	return j.run, nil
}
func (j *faultJournal) Append(_ context.Context, r domain.CompactionRun) error {
	if r.Phase == j.failAppendPhase {
		return errors.New("append fault")
	}
	j.run = r
	return nil
}

type faultBuilder struct{ fail string }

func (b faultBuilder) Build(context.Context, string, string) error {
	if b.fail == "build" {
		return errors.New("build fault")
	}
	return nil
}
func (b faultBuilder) ClassifyCandidate(context.Context, string, string) (domain.CandidateCondition, error) {
	return domain.CandidateConditionComplete, nil
}
func (b faultBuilder) Sync(context.Context, string) error {
	if b.fail == "sync" {
		return errors.New("sync fault")
	}
	return nil
}
func (b faultBuilder) VerifyPair(context.Context, string, string) error {
	if b.fail == "verify" {
		return errors.New("verify fault")
	}
	return nil
}

type faultFiles struct {
	fail        string
	observation domain.CompactionObservation
}

func (f faultFiles) PrepareCandidate(context.Context, domain.CompactionRun) (domain.StoreFileIdentity, error) {
	if f.fail == "prepare" {
		return domain.StoreFileIdentity{}, errors.New("prepare fault")
	}
	return domain.StoreFileIdentity{Device: 1, Inode: 9}, nil
}

func (f faultFiles) Plan(_ context.Context, r domain.CompactionRun) (domain.CompactionRun, error) {
	return r, nil
}
func (f faultFiles) Recheck(context.Context, domain.CompactionRun) error {
	if f.fail == "recheck" {
		return errors.New("recheck fault")
	}
	return nil
}
func (f faultFiles) RemoveOwnedPartialCandidate(context.Context, domain.CompactionRun, domain.CompactionObservation) error {
	if f.fail == "cleanup" {
		return errors.New("cleanup fault")
	}
	return nil
}
func (f faultFiles) FenceCandidate(_ context.Context, r domain.CompactionRun) (domain.CompactionRun, error) {
	if f.fail == "fence" {
		return r, errors.New("fence fault")
	}
	r.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 9}
	return r, nil
}
func (f faultFiles) Exchange(context.Context, domain.CompactionRun) error {
	if f.fail == "exchange" {
		return errors.New("exchange fault")
	}
	return nil
}
func (f faultFiles) PublishRollback(context.Context, domain.CompactionRun) error {
	if f.fail == "publish" {
		return errors.New("publish fault")
	}
	return nil
}
func (f faultFiles) Observe(context.Context, domain.CompactionRun) (domain.CompactionObservation, error) {
	if f.fail == "observe" {
		return f.observation, errors.New("observe fault")
	}
	return f.observation, nil
}

func (f faultFiles) SyncRecoveredOrientation(context.Context, domain.CompactionRun, domain.CompactionObservation) error {
	if f.fail == "sync_recovered" {
		return errors.New("sync recovered fault")
	}
	return nil
}

type faultLease struct{ fail bool }

func (l faultLease) AcquireExclusive(context.Context, string) (func(), error) {
	if l.fail {
		return nil, errors.New("lease fault")
	}
	return func() {}, nil
}

func compactionRunAt(phase domain.CompactionPhase) domain.CompactionRun {
	now := time.Now()
	return domain.CompactionRun{ID: "0123456789abcdef0123456789abcdef", SourcePath: "/store", CandidatePath: "/candidate", RollbackPath: "/rollback", SourceIdentity: domain.StoreFileIdentity{Device: 1, Inode: 1}, Phase: phase, CreatedAt: now, UpdatedAt: now}
}

func TestStoreCompactionApplyFaultMatrix(t *testing.T) {
	cases := []struct {
		name, fail string
		phase      domain.CompactionPhase
		append     domain.CompactionPhase
		lease      bool
		load       bool
	}{
		{"journal load", "", domain.CompactionPlanned, "", false, true}, {"lease", "", domain.CompactionPlanned, "", true, false}, {"resource recheck", "recheck", domain.CompactionPlanned, "", false, false}, {"journal intent", "", domain.CompactionPlanned, domain.CompactionCopyIntent, false, false}, {"copy", "build", domain.CompactionCopyIntent, "", false, false}, {"sync", "sync", domain.CompactionCandidateSyncIntent, "", false, false}, {"scrub", "verify", domain.CompactionScrubInProgress, "", false, false}, {"candidate fence", "fence", domain.CompactionScrubInProgress, "", false, false}, {"exchange", "exchange", domain.CompactionSwapIntent, "", false, false}, {"rollback publish", "publish", domain.CompactionRollbackPublishIntent, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := &faultJournal{run: compactionRunAt(tc.phase), failLoad: tc.load, failAppendPhase: tc.append}
			service := NewStoreCompactionUsecase("/store", j, faultBuilder{fail: tc.fail}, faultFiles{fail: tc.fail}, faultLease{fail: tc.lease})
			if _, err := service.Apply(context.Background(), j.run.ID); err == nil {
				t.Fatal("fault boundary unexpectedly succeeded")
			}
		})
	}
}

func TestStoreCompactionResumeConvergesFromObservedSwappedWithNewInstance(t *testing.T) {
	run := compactionRunAt(domain.CompactionSwapIntent)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
	j := &faultJournal{run: run}
	observation := domain.CompactionObservation{Orientation: domain.OrientationSwapped, Source: run.Candidate, Candidate: run.SourceIdentity, CandidateExists: true}
	service := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{observation: observation}, faultLease{})
	got, err := service.Resume(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", got.Phase)
	}
}

func TestStoreCompactionResumeExchangesFencedCandidateReadyAtSwapIntent(t *testing.T) {
	run := compactionRunAt(domain.CompactionSwapIntent)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
	j := &faultJournal{run: run}
	observation := domain.CompactionObservation{Orientation: domain.OrientationCandidateReady, Source: run.SourceIdentity, Candidate: run.Candidate, CandidateExists: true}
	service := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{observation: observation}, faultLease{})
	got, err := service.Resume(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", got.Phase)
	}
}

func TestStoreCompactionRejectsRunForAnotherDatabaseInSameDirectory(t *testing.T) {
	run := compactionRunAt(domain.CompactionPlanned)
	run.SourcePath = "/same-dir/a.db"
	j := &faultJournal{run: run}
	service := NewStoreCompactionUsecase("/same-dir/b.db", j, faultBuilder{}, faultFiles{}, faultLease{})
	if _, err := service.Status(context.Background(), run.ID); err == nil {
		t.Fatal("Status accepted run bound to another database")
	}
	if _, err := service.Apply(context.Background(), run.ID); err == nil {
		t.Fatal("Apply accepted run bound to another database")
	}
}

func TestStoreCompactionAfterBoundaryStopsResumeWithNewInstance(t *testing.T) {
	cases := []struct {
		name              string
		phase, failAppend domain.CompactionPhase
		candidate         bool
		orientation       domain.CompactionOrientation
	}{
		{"copy after", domain.CompactionCopyIntent, domain.CompactionCopyComplete, false, domain.OrientationCandidateReady},
		{"sync and fsync after", domain.CompactionCandidateSyncIntent, domain.CompactionCandidateSynced, false, domain.OrientationCandidateReady},
		{"scrub and fence after", domain.CompactionScrubInProgress, domain.CompactionCandidateVerified, false, domain.OrientationCandidateReady},
		{"swap and directory fsync after", domain.CompactionSwapIntent, domain.CompactionSwapped, true, domain.OrientationSwapped},
		{"publish after", domain.CompactionRollbackPublishIntent, domain.CompactionRollbackReady, true, domain.OrientationRollbackReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := compactionRunAt(tc.phase)
			if tc.candidate {
				run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
			}
			j := &faultJournal{run: run, failAppendPhase: tc.failAppend}
			first := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{}, faultLease{})
			if _, err := first.Apply(context.Background(), run.ID); err == nil {
				t.Fatal("injected after-boundary stop did not fail")
			}
			j.failAppendPhase = ""
			observation := domain.CompactionObservation{Orientation: tc.orientation, Source: run.SourceIdentity, Candidate: run.Candidate, CandidateExists: tc.orientation == domain.OrientationCandidateReady || tc.orientation == domain.OrientationSwapped, RollbackExists: tc.orientation == domain.OrientationRollbackReady}
			if tc.orientation == domain.OrientationCandidateReady && j.run.PreparedCandidateIdentity != (domain.StoreFileIdentity{}) {
				observation.Candidate = j.run.PreparedCandidateIdentity
			}
			if tc.orientation == domain.OrientationSwapped {
				observation.Source = run.Candidate
				observation.Candidate = run.SourceIdentity
			}
			if tc.orientation == domain.OrientationRollbackReady {
				observation.Source = run.Candidate
				observation.Rollback = run.SourceIdentity
			}
			resumed := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{observation: observation}, faultLease{})
			got, err := resumed.Resume(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Phase != domain.CompactionCommitted {
				t.Fatalf("phase=%s", got.Phase)
			}
		})
	}
}

func TestStoreCompactionRollbackAfterExchangeStopResumesRolledBack(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
	j := &faultJournal{run: run, failAppendPhase: domain.CompactionRollbackSwapped}
	ready := domain.CompactionObservation{Orientation: domain.OrientationRollbackReady, Source: run.Candidate, Rollback: run.SourceIdentity, RollbackExists: true}
	first := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{observation: ready}, faultLease{})
	if _, err := first.Rollback(context.Background(), run.ID); err == nil {
		t.Fatal("rollback after-exchange stop did not fail")
	}
	j.failAppendPhase = ""
	rolled := domain.CompactionObservation{Orientation: domain.OrientationRolledBack, Source: run.SourceIdentity, Rollback: run.Candidate, RollbackExists: true}
	resumed := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{observation: rolled}, faultLease{})
	got, err := resumed.Resume(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionRolledBack {
		t.Fatalf("phase=%s", got.Phase)
	}
}

func TestStoreCompactionRollbackBeforeExchangeFaultKeepsIntentResumable(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
	j := &faultJournal{run: run}
	ready := domain.CompactionObservation{Orientation: domain.OrientationRollbackReady, Source: run.Candidate, Rollback: run.SourceIdentity, RollbackExists: true}
	first := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{fail: "exchange", observation: ready}, faultLease{})
	if _, err := first.Rollback(context.Background(), run.ID); err == nil {
		t.Fatal("rollback exchange fault unexpectedly succeeded")
	}
	if j.run.Phase != domain.CompactionRollbackSwapIntent {
		t.Fatalf("phase=%s", j.run.Phase)
	}
	resumed := NewStoreCompactionUsecase("/store", j, faultBuilder{}, faultFiles{observation: ready}, faultLease{})
	got, err := resumed.Resume(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionRolledBack {
		t.Fatalf("phase=%s", got.Phase)
	}
}
