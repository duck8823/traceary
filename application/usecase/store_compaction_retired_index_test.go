package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain"
)

// TestStoreCompactionRefusesToPublishARetiredSearchIndex covers the resume
// path that the plan-time and build-time guards both miss. A run journaled by
// an older binary carries no verdict about the legacy search family, and
// resuming one from CandidateVerified goes straight to the exchange without
// revisiting Plan or Build — so without this check the dead index would be
// published into the live store anyway.
func TestStoreCompactionRefusesToPublishARetiredSearchIndex(t *testing.T) {
	run := compactionRunAt(domain.CompactionCandidateVerified)
	run.Candidate = domain.StoreFileIdentity{Device: 1, Inode: 2}
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
	if journal.run.Phase != domain.CompactionCandidateVerified {
		t.Fatalf("run advanced to %s; the refusal must stop the run before the exchange", journal.run.Phase)
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
