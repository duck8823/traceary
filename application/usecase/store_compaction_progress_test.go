package usecase

import (
	"context"
	"testing"

	"github.com/duck8823/traceary/domain"
)

type recordProgress struct {
	phases  []domain.CompactionPhase
	watches int
}

func (r *recordProgress) OnPhase(phase domain.CompactionPhase, _ uint64) {
	r.phases = append(r.phases, phase)
}

func (r *recordProgress) WatchBuild(context.Context, string, uint64) func() {
	r.watches++
	return func() {}
}

func TestBindCompactionProgress_ReportsPhaseAndBuildWatch(t *testing.T) {
	t.Parallel()
	source := "/store"
	run := compactionRunAt(domain.CompactionCandidatePrepared)
	run.SourcePath = source
	run.CandidatePath = source + ".cand"
	progress := &recordProgress{}
	svc := NewStoreCompactionUsecase(source, &faultJournal{run: run}, faultBuilder{}, faultFiles{
		observation: domain.CompactionObservation{Orientation: domain.OrientationCandidateReady, CandidateCondition: domain.CandidateConditionOwnedIncomplete},
	}, faultLease{})
	BindCompactionProgress(svc, progress)
	if _, err := svc.Apply(context.Background(), run.ID); err != nil && progress.watches == 0 && len(progress.phases) == 0 {
		t.Fatalf("progress unused: apply err=%v phases=%v watches=%d", err, progress.phases, progress.watches)
	}
	if progress.watches == 0 {
		t.Fatalf("WatchBuild was not called; phases=%v", progress.phases)
	}
}
