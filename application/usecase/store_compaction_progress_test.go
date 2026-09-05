package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

type recordProgress struct {
	phases  []domain.CompactionPhase
	windows []string
	watches int
}

func (r *recordProgress) OnWindow(name string) {
	r.windows = append(r.windows, name)
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

func TestCompact_ReportsPreCopyWindows(t *testing.T) {
	source := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(source, []byte("not-a-real-store"), 0o600); err != nil {
		t.Fatal(err)
	}
	progress := &recordProgress{}
	svc := NewStoreCompactionUsecase(source, &faultJournal{run: compactionRunAt(domain.CompactionCommitted)}, faultBuilder{}, faultFiles{}, faultLease{})
	BindCompactionProgress(svc, progress)
	_, _ = svc.Compact(context.Background(), application.CompactInput{Source: source})
	want := []string{"exclusive_lease", "inspect_reclaim", "plan"}
	if len(progress.windows) < len(want) {
		t.Fatalf("windows=%v, want at least %v", progress.windows, want)
	}
	for i, name := range want {
		if progress.windows[i] != name {
			t.Fatalf("windows[%d]=%q, want %q (all=%v)", i, progress.windows[i], name, progress.windows)
		}
	}
}
