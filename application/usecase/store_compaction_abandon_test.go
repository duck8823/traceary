package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/duck8823/traceary/domain"
)

type findingJournal struct {
	faultJournal
	inFlight domain.CompactionRun
	missing  bool
	phases   []domain.CompactionPhase
}

func (j *findingJournal) FindInFlight(context.Context, string) (domain.CompactionRun, error) {
	if j.missing || j.inFlight.Phase.IsTerminal() {
		return domain.CompactionRun{}, os.ErrNotExist
	}
	if j.inFlight.ID != "" {
		return j.inFlight, nil
	}
	if j.run.ID != "" && !j.run.Phase.IsTerminal() {
		return j.run, nil
	}
	return domain.CompactionRun{}, os.ErrNotExist
}

func (j *findingJournal) Append(ctx context.Context, r domain.CompactionRun) error {
	j.phases = append(j.phases, r.Phase)
	if r.ID == j.inFlight.ID {
		j.inFlight = r
	}
	return j.faultJournal.Append(ctx, r)
}

type abandonFiles struct {
	faultFiles
	current      domain.StoreFileIdentity
	removed      []string
	inspectErr   error
	removeErr    error
	inspectCalls int
	removeCalls  int
}

func (f *abandonFiles) InspectStoreFile(context.Context, string) (domain.StoreFileIdentity, error) {
	f.inspectCalls++
	if f.inspectErr != nil {
		return domain.StoreFileIdentity{}, f.inspectErr
	}
	return f.current, nil
}

func (f *abandonFiles) RemoveAbandonedCandidate(_ context.Context, run domain.CompactionRun) error {
	f.removeCalls++
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, run.CandidatePath)
	return nil
}

func TestCompactLeasedDoesNotAbandonSwapIntent(t *testing.T) {
	run := compactionRunAt(domain.CompactionSwapIntent)
	run.SourcePath = "/store"
	current := run.SourceIdentity
	current.ModUnix++
	journal := &findingJournal{inFlight: run}
	files := &abandonFiles{
		faultFiles: faultFiles{fail: "observe", observation: domain.CompactionObservation{}},
		current:    current,
	}
	svc := NewStoreCompactionUsecase("/store", journal, faultBuilder{}, files, faultLease{})
	if _, err := svc.Resume(context.Background(), run.ID); err == nil {
		t.Fatal("swap_intent resume must still observe")
	}
	if files.removeCalls != 0 {
		t.Fatalf("removeCalls=%d, want 0", files.removeCalls)
	}
}

func TestAbandonStalePrePublicationNoopsWhenSourceMatches(t *testing.T) {
	run := compactionRunAt(domain.CompactionCandidatePrepared)
	run.SourcePath = "/store"
	journal := &findingJournal{inFlight: run}
	files := &abandonFiles{current: run.SourceIdentity}
	svc := NewStoreCompactionUsecase("/store", journal, faultBuilder{}, files, faultLease{})
	got, err := svc.AbandonStalePrePublication(context.Background(), "/store")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" {
		t.Fatalf("got=%+v, want empty (source still matches)", got)
	}
	if files.removeCalls != 0 {
		t.Fatalf("removeCalls=%d, want 0", files.removeCalls)
	}
}

func TestBuildCancellationJournalsAbandoned(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := compactionRunAt(domain.CompactionCandidatePrepared)
	run.SourcePath = "/store"
	journal := &faultJournal{run: run}
	files := &abandonFiles{current: run.SourceIdentity}
	svc := NewStoreCompactionUsecase("/store", journal, cancelBuild{}, files, faultLease{}).(*storeCompactionUsecase)
	got, err := svc.applyLeased(ctx, run)
	if err == nil {
		t.Fatal("expected canceled build error")
	}
	if got.Phase != domain.CompactionAbandoned && journal.run.Phase != domain.CompactionAbandoned {
		t.Fatalf("phase got=%q journal=%q, want abandoned", got.Phase, journal.run.Phase)
	}
	if files.removeCalls != 1 {
		t.Fatalf("removeCalls=%d, want 1", files.removeCalls)
	}
}

type cancelBuild struct{ faultBuilder }

func (cancelBuild) Build(ctx context.Context, _, _ string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("build canceled: %w", err)
	}
	return errors.New("build was not canceled")
}

func TestCompactLeasedAbandonUsesMovedSource(t *testing.T) {
	stale := compactionRunAt(domain.CompactionCandidatePrepared)
	stale.SourcePath = "/store"
	stale.CandidatePath = "/store.compact-" + stale.ID
	current := stale.SourceIdentity
	current.ModUnix++
	journal := &findingJournal{faultJournal: faultJournal{run: stale}, inFlight: stale}
	files := &abandonFiles{current: current}
	svc := NewStoreCompactionUsecase("/store", journal, faultBuilder{fail: "build"}, files, faultLease{}).(*storeCompactionUsecase)
	_, err := svc.compactLeased(context.Background(), "/store")
	if err == nil {
		t.Fatal("fresh apply should fail at build")
	}
	if len(journal.phases) == 0 || journal.phases[0] != domain.CompactionAbandoned {
		t.Fatalf("phases=%v, want abandoned first", journal.phases)
	}
	if journal.inFlight.Phase != domain.CompactionAbandoned {
		t.Fatalf("in-flight phase=%q, want abandoned", journal.inFlight.Phase)
	}
	if files.removeCalls != 1 || files.removed[0] != stale.CandidatePath {
		t.Fatalf("removed=%v", files.removed)
	}
}

func TestAbandonPrePublicationRefusesSwapIntent(t *testing.T) {
	run := compactionRunAt(domain.CompactionSwapIntent)
	svc := NewStoreCompactionUsecase("/store", &faultJournal{run: run}, faultBuilder{}, &abandonFiles{}, faultLease{}).(*storeCompactionUsecase)
	if _, err := svc.abandonPrePublication(context.Background(), run); err == nil {
		t.Fatal("expected refuse")
	}
}
