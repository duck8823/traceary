package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain"
)

// TestStoreCompactionRefusesToPublishARetiredSearchIndex covers the resume
// paths that the plan-time and build-time guards both miss. A run journaled by
// an older binary carries no verdict about the legacy search family, and
// resuming one goes to the exchange without revisiting Plan or Build — so
// without this check the dead index would be published into the live store.
//
// swap_intent is the case that a phase-entry check does not catch:
// RecoveryActions answers [ActionExchange, ActionRecordSwapped] and
// resumeLeased performs the exchange itself, before applyLeased is ever
// reached. Both journalled phases must be refused, and neither may advance.
func TestStoreCompactionRefusesToPublishARetiredSearchIndex(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase domain.CompactionPhase
	}{
		{name: "verified candidate not yet exchanged", phase: domain.CompactionCandidateVerified},
		{name: "swap intent recovered by resume", phase: domain.CompactionSwapIntent},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			run := compactionRunAt(tc.phase)
			run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
			run.PreparedCandidateIdentity = run.Candidate
			journal := &faultJournal{run: run}
			observation := domain.CompactionObservation{
				Orientation:     domain.OrientationCandidateReady,
				Source:          run.SourceIdentity,
				Candidate:       run.Candidate,
				CandidateExists: true,
			}

			sut := NewStoreCompactionUsecase(
				"/store",
				journal,
				faultBuilder{},
				faultFiles{fail: "retired-search-index", observation: observation},
				faultLease{},
			)
			_, err := sut.Resume(context.Background(), run.ID)
			if err == nil {
				t.Fatal("Resume() error = nil, want a refusal to publish a candidate carrying the retired search family")
			}
			if !strings.Contains(err.Error(), "retired search index") {
				t.Fatalf("Resume() error = %v, want the retired-search-index refusal", err)
			}
			// The apply path records swap_intent before it reaches the
			// exchange, and a recorded intent publishes nothing — a run
			// parked there is refused again on the next resume. What must
			// never happen is the swap itself.
			switch journal.run.Phase {
			case domain.CompactionSwapped, domain.CompactionCommitted:
				t.Fatalf("run reached %s; the candidate was published despite the refusal", journal.run.Phase)
			}
		})
	}
}

// TestStoreCompactionFinishesASwapItAlreadyPublished is the other half of the
// same rule. Past the exchange the candidate is the live store, so refusing
// would strand the run half-published — a far worse outcome than a store that
// still carries an index the operator can retire afterwards.
func TestStoreCompactionFinishesASwapItAlreadyPublished(t *testing.T) {
	run := compactionRunAt(domain.CompactionSwapped)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
	journal := &faultJournal{run: run}
	observation := domain.CompactionObservation{
		Orientation:     domain.OrientationSwapped,
		Source:          run.Candidate,
		Candidate:       run.SourceIdentity,
		CandidateExists: true,
	}

	sut := NewStoreCompactionUsecase(
		"/store",
		journal,
		faultBuilder{},
		faultFiles{fail: "retired-search-index", observation: observation},
		faultLease{},
	)
	got, err := sut.Resume(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Resume() error = %v, want the already-published swap to be finished", err)
	}
	if got.Phase != domain.CompactionCommitted {
		t.Fatalf("Resume() phase = %s, want %s", got.Phase, domain.CompactionCommitted)
	}
}

// TestStoreCompactionRollbackIsNotBlockedByTheRetiredSearchIndex pins the
// direction of the guard. Rollback exchanges the original source back into
// place; the family it carries is exactly what the operator is rolling back
// to. Refusing here would trap the store on a candidate it is abandoning.
func TestStoreCompactionRollbackIsNotBlockedByTheRetiredSearchIndex(t *testing.T) {
	run := compactionRunAt(domain.CompactionCommitted)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
	journal := &faultJournal{run: run}
	ready := domain.CompactionObservation{
		Orientation:    domain.OrientationRollbackReady,
		Source:         run.Candidate,
		Rollback:       run.SourceIdentity,
		RollbackExists: true,
	}

	sut := NewStoreCompactionUsecase(
		"/store",
		journal,
		faultBuilder{},
		faultFiles{fail: "retired-search-index", observation: ready},
		faultLease{},
	)
	got, err := sut.Rollback(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Rollback() error = %v, want the rollback to complete", err)
	}
	if got.Phase != domain.CompactionRolledBack {
		t.Fatalf("Rollback() phase = %s, want %s", got.Phase, domain.CompactionRolledBack)
	}
}
