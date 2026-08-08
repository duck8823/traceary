package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type orphanRepoStub struct {
	candidates   []*model.SessionOrphanRange
	discoverErr  error
	material     model.SessionOrphanMaterial
	materialErr  error
	loadCalls    int
	discoverCall int
}

func (s *orphanRepoStub) Record(context.Context, *model.SessionOrphanRange) error {
	return nil
}

func (s *orphanRepoStub) DiscoverCandidates(context.Context, time.Duration, time.Time) ([]*model.SessionOrphanRange, error) {
	s.discoverCall++
	if s.discoverErr != nil {
		return nil, s.discoverErr
	}
	return s.candidates, nil
}

func (s *orphanRepoStub) LoadMaterial(
	context.Context,
	types.SessionID,
	types.Optional[types.EventID],
	types.EventID,
) (model.SessionOrphanMaterial, error) {
	s.loadCalls++
	if s.materialErr != nil {
		return model.SessionOrphanMaterial{}, s.materialErr
	}
	return s.material, nil
}

type refineStubForOrphan struct {
	calls []usecase.SessionRefineInput
	err   error
}

func (s *refineStubForOrphan) Refine(_ context.Context, input usecase.SessionRefineInput) (model.SessionRefineResult, error) {
	s.calls = append(s.calls, input)
	if s.err != nil {
		return model.SessionRefineResult{}, s.err
	}
	row, err := model.NewSessionRefinement(
		input.SessionID, 1, "from", input.CoversTo, input.Summary, input.Keywords,
		input.ProducedBy, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), input.Degraded,
	)
	if err != nil {
		//nolint:wrapcheck // test stub surfaces factory failure as-is
		return model.SessionRefineResult{}, err
	}
	//nolint:wrapcheck // test stub surfaces factory failure as-is
	return model.SessionRefineResultOf(model.SessionRefineOutcomeCreated, row)
}

type fixedOrphanClock struct{ at time.Time }

func (c fixedOrphanClock) Now() time.Time { return c.at }

func TestOrphanConsolidationUsecase_ProducesDegradedRefinement(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	orphan, err := model.NewSessionOrphanRange(
		"sess-1",
		types.Some(types.EventID("evt-a")),
		"evt-c",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{orphan},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now.Add(-time.Hour),
			LastCreatedAt:  now,
			KindCounts:     map[string]int{"prompt": 2, "command_executed": 1},
			Commands:       []string{"go test ./..."},
			EventCount:     3,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		DryRun:     false,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if diff := cmp.Diff(1, got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if len(refine.calls) != 1 {
		t.Fatalf("Refine calls = %d, want 1", len(refine.calls))
	}
	call := refine.calls[0]
	if !call.Degraded {
		t.Fatal("Degraded = false, want true")
	}
	if call.ProducedBy != "gc:orphan-consolidation" {
		t.Fatalf("ProducedBy = %q", call.ProducedBy)
	}
	if call.CoversTo != "evt-c" {
		t.Fatalf("CoversTo = %s, want evt-c", call.CoversTo)
	}
	if !strings.Contains(call.Summary, "Mechanical summary") {
		t.Fatalf("summary missing mechanical header: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "does not recover agent reasoning") {
		t.Fatalf("summary must state reasoning is gone: %q", call.Summary)
	}
	if strings.Contains(strings.ToLower(call.Summary), "nothing is lost") {
		t.Fatalf("summary must not claim nothing is lost: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "prompt: 2") || !strings.Contains(call.Summary, "go test ./...") {
		t.Fatalf("summary missing kind/command detail: %q", call.Summary)
	}
}

func TestOrphanConsolidationUsecase_DryRunCountsWithoutWriting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	orphan, err := model.NewSessionOrphanRange("sess-1", types.None[types.EventID](), "evt-z", now)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{orphan},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now,
			LastCreatedAt:  now,
			KindCounts:     map[string]int{"note": 1},
			EventCount:     1,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	want := apptypes.OrphanConsolidationResultOf(1, true)
	if diff := cmp.Diff(want.ProducedCount(), got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if !got.DryRun() {
		t.Fatal("DryRun() = false, want true")
	}
	if len(refine.calls) != 0 {
		t.Fatalf("Refine calls = %d, want 0 on dry-run", len(refine.calls))
	}
	if repo.loadCalls != 1 {
		t.Fatalf("LoadMaterial calls = %d, want 1 (validate material exists)", repo.loadCalls)
	}
}

func TestOrphanConsolidationUsecase_SecondRunDoesNotReprocessWhenNoCandidates(t *testing.T) {
	t.Parallel()

	// After a successful fold, discovery returns nothing — the covered range
	// is filtered at the repository. A second Consolidate must not Refine.
	repo := &orphanRepoStub{candidates: nil}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, types.SystemClock{})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != 0 || len(refine.calls) != 0 {
		t.Fatalf("produced=%d refineCalls=%d, want 0/0", got.ProducedCount(), len(refine.calls))
	}
}

func TestBuildMechanicalOrphanSummary_TruncatesDeterministically(t *testing.T) {
	t.Parallel()

	// Force truncation by stuffing many command lines.
	commands := make([]string, 0, 4000)
	for i := 0; i < 4000; i++ {
		commands = append(commands, strings.Repeat("x", 40))
	}
	material := model.SessionOrphanMaterial{
		FirstCreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastCreatedAt:  time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		KindCounts:     map[string]int{"note": 1},
		Commands:       commands,
		EventCount:     1,
	}
	// buildMechanicalOrphanSummary is package-private; exercise via Consolidate.
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	orphan, err := model.NewSessionOrphanRange("sess", types.None[types.EventID](), "evt", now)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{orphan},
		material:   material,
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})
	if _, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{StaleAfter: time.Hour}); err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if len(refine.calls) != 1 {
		t.Fatalf("Refine calls = %d", len(refine.calls))
	}
	summary := refine.calls[0].Summary
	if len(summary) > 64*1024 {
		t.Fatalf("summary len = %d, want <= 64KiB", len(summary))
	}
	if !strings.Contains(summary, "truncated") {
		t.Fatalf("summary should declare truncation: %q", summary[:200])
	}
}
