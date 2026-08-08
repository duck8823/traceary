package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var errOrphanMaterialStub = errors.New("orphan material load failed")
var errOrphanRefineStub = errors.New("orphan refine failed")

type orphanRepoStub struct {
	candidates   []*model.SessionOrphanRange
	hasMore      bool
	discoverErr  error
	material     model.SessionOrphanMaterial
	materialErr  error
	// materialErrBySession maps session id → LoadMaterial error for that session.
	materialErrBySession map[types.SessionID]error
	// produce via refine is controlled by refineStub; LoadMaterial path uses materialErr*.
	loadCalls    int
	discoverCall int
	lastLimit    int
}

func (s *orphanRepoStub) Record(context.Context, *model.SessionOrphanRange) error {
	return nil
}

func (s *orphanRepoStub) DiscoverCandidates(_ context.Context, _ time.Duration, _ time.Time, limit int) (model.SessionOrphanCandidates, error) {
	s.discoverCall++
	s.lastLimit = limit
	if s.discoverErr != nil {
		return model.SessionOrphanCandidates{}, s.discoverErr
	}
	ranges := s.candidates
	hasMore := s.hasMore
	if limit > 0 && len(ranges) > limit {
		ranges = ranges[:limit]
		hasMore = true
	}
	return model.SessionOrphanCandidates{Ranges: ranges, HasMore: hasMore}, nil
}

func (s *orphanRepoStub) LoadMaterial(
	_ context.Context,
	sessionID types.SessionID,
	_ types.Optional[types.EventID],
	_ types.EventID,
) (model.SessionOrphanMaterial, error) {
	s.loadCalls++
	if s.materialErrBySession != nil {
		if err, ok := s.materialErrBySession[sessionID]; ok {
			return model.SessionOrphanMaterial{}, err
		}
	}
	if s.materialErr != nil {
		return model.SessionOrphanMaterial{}, s.materialErr
	}
	return s.material, nil
}

// refineStubForOrphan records Refine inputs. When ComposeSummary is set it is
// applied against current so callers still assert finished summary text without
// reimplementing the real CAS loop.
type refineStubForOrphan struct {
	calls   []usecase.SessionRefineInput
	err     error
	current types.Optional[*model.SessionRefinement]
}

func (s *refineStubForOrphan) Refine(_ context.Context, input usecase.SessionRefineInput) (model.SessionRefineResult, error) {
	resolved := input
	if input.ComposeSummary != nil {
		summary, keywords := input.ComposeSummary(s.current)
		resolved.Summary = summary
		resolved.Keywords = keywords
		// Clear the func so stored calls stay comparable and free of captures.
		resolved.ComposeSummary = nil
	}
	s.calls = append(s.calls, resolved)
	if s.err != nil {
		return model.SessionRefineResult{}, s.err
	}
	row, err := model.NewSessionRefinement(
		resolved.SessionID, 1, "from", resolved.CoversTo, resolved.Summary, resolved.Keywords,
		resolved.ProducedBy, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), resolved.Degraded,
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

// steppingOrphanClock advances by step on every Now() call after the first return value.
type steppingOrphanClock struct {
	at   time.Time
	step time.Duration
	n    int
}

func (c *steppingOrphanClock) Now() time.Time {
	t := c.at.Add(time.Duration(c.n) * c.step)
	c.n++
	return t
}

func mustOrphanRange(t *testing.T, sessionID string, to string, at time.Time) *model.SessionOrphanRange {
	t.Helper()
	orphan, err := model.NewSessionOrphanRange(types.SessionID(sessionID), types.None[types.EventID](), types.EventID(to), at)
	if err != nil {
		t.Fatal(err)
	}
	return orphan
}

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

func TestOrphanConsolidationUsecase_ComposesOntoExistingAgentSummary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	sessionID := types.SessionID("sess-compose")
	const agentSummary = "Agent folded events 1..100: decided to split the refactor into three PRs because of shared types."
	const agentKeywords = "refactor,pr-split"
	existing, err := model.NewSessionRefinement(
		sessionID, 1, "evt-1", "evt-100", agentSummary, agentKeywords,
		"agent", now.Add(-time.Hour), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := model.NewSessionOrphanRange(
		sessionID,
		types.Some(types.EventID("evt-100")),
		"evt-150",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{orphan},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now.Add(-30 * time.Minute),
			LastCreatedAt:  now,
			KindCounts:     map[string]int{"note": 50},
			EventCount:     50,
		},
	}
	refine := &refineStubForOrphan{current: types.Some(existing)}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}
	if len(refine.calls) != 1 {
		t.Fatalf("Refine calls = %d, want 1", len(refine.calls))
	}
	call := refine.calls[0]

	// Agent prose must survive wholesale; mechanical section is appended after a seam.
	if !strings.Contains(call.Summary, agentSummary) {
		t.Fatalf("summary lost agent text: %q", call.Summary)
	}
	if !strings.HasPrefix(call.Summary, agentSummary) {
		t.Fatalf("agent text must lead the composed summary: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "---") {
		t.Fatalf("summary missing seam marker: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "degraded section") {
		t.Fatalf("summary missing degraded section label: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "after evt-100 through evt-150") {
		t.Fatalf("summary missing orphan coverage range: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "Mechanical summary") {
		t.Fatalf("summary missing mechanical body: %q", call.Summary)
	}
	if !strings.Contains(call.Summary, "note: 50") {
		t.Fatalf("summary missing mechanical kind counts: %q", call.Summary)
	}
	// Coverage and text must agree: Refine advances covers_to to the orphan tail
	// while covers_from is kept by the refinement use case (not visible on input).
	if call.CoversTo != "evt-150" {
		t.Fatalf("CoversTo = %s, want evt-150", call.CoversTo)
	}
	if call.Keywords != agentKeywords {
		t.Fatalf("Keywords = %q, want preserved agent keywords", call.Keywords)
	}
	// Pin degraded=true on mixed rows: the flag means not purely agent-authored.
	if !call.Degraded {
		t.Fatal("Degraded = false on composed row, want true (mixed authorship)")
	}
	if call.ProducedBy != "gc:orphan-consolidation" {
		t.Fatalf("ProducedBy = %q", call.ProducedBy)
	}
}

func TestOrphanConsolidationUsecase_CompositionObservesCASReRead(t *testing.T) {
	t.Parallel()

	// Composition must use the row re-read on each CAS attempt. If the finished
	// string is frozen before Refine, a lost race rewrites agent prose that
	// landed between the pre-read and the successful write.
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	sessionID := types.SessionID("sess-cas-compose")
	evt1 := types.EventID("evt-1")
	evt100 := types.EventID("evt-100")
	evt120 := types.EventID("evt-120")
	evt150 := types.EventID("evt-150")
	const firstAgent = "FIRST-agent prose that must not survive the race"
	const secondAgent = "SECOND-agent prose that landed during the lost CAS"
	const secondKeywords = "second,keywords"

	firstRow := mustRefinement(t, sessionID, 1, evt1, evt100, firstAgent)
	// Race winner advances generation and covers_to but stays behind the orphan
	// tail so gc still has a legitimate supersede to land on retry.
	winner, err := model.NewSessionRefinement(
		sessionID, 2, evt1, evt120, secondAgent, secondKeywords,
		"agent", now.Add(-time.Minute), false,
	)
	if err != nil {
		t.Fatal(err)
	}

	orphan, err := model.NewSessionOrphanRange(
		sessionID,
		types.Some(evt100),
		evt150,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	orphanRepo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{orphan},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now.Add(-time.Hour),
			LastCreatedAt:  now,
			KindCounts:     map[string]int{"note": 3},
			EventCount:     3,
		},
	}

	eventOrder := &sessionEventOrderRepositoryStub{
		earliest:      map[types.SessionID]types.EventID{sessionID: evt1},
		eventSessions: map[types.EventID]types.SessionID{evt150: sessionID},
		after: map[types.EventID]map[types.EventID]bool{
			evt150: {evt100: true, evt120: true},
			evt120: {evt100: true},
		},
	}
	refineRepo := &sessionRefinementRepositoryStub{
		bySession: map[types.SessionID]*model.SessionRefinement{
			sessionID: firstRow,
		},
		saveResults:       []bool{false, true},
		raceWinnersOnLose: []*model.SessionRefinement{winner},
		eventOrder:        eventOrder,
	}
	session := model.NewSession(sessionID, now.Add(-2*time.Hour), "cli", "codex", "ws")
	refine := usecase.NewSessionRefinementUsecase(
		&sessionRepositoryStub{session: session},
		refineRepo,
		eventOrder,
		fixedOrphanClock{at: now},
	)
	sut := usecase.NewOrphanConsolidationUsecase(orphanRepo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}
	if refineRepo.saveCalls != 2 {
		t.Fatalf("SaveIfAdvances calls = %d, want 2 (first CAS loss, second success)", refineRepo.saveCalls)
	}
	if len(refineRepo.saved) != 1 {
		t.Fatalf("successful saves = %d, want 1", len(refineRepo.saved))
	}
	summary := refineRepo.saved[0].Summary()
	if !strings.Contains(summary, secondAgent) {
		t.Fatalf("final summary must contain second-read agent text; got %q", summary)
	}
	if strings.Contains(summary, firstAgent) {
		t.Fatalf("final summary must not keep first-read agent text after race; got %q", summary)
	}
	if !strings.HasPrefix(summary, secondAgent) {
		t.Fatalf("second-read agent text must lead the composed summary: %q", summary)
	}
	if refineRepo.saved[0].Keywords() != secondKeywords {
		t.Fatalf("Keywords = %q, want second-read keywords %q", refineRepo.saved[0].Keywords(), secondKeywords)
	}
	if diff := cmp.Diff([]int{1, 2}, refineRepo.expectedGens); diff != "" {
		t.Fatalf("expectedGeneration sequence mismatch (-want +got):\n%s", diff)
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
	want := apptypes.OrphanConsolidationResultOf(1, 1, 0, false, true)
	if diff := cmp.Diff(want.ProducedCount(), got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if !got.DryRun() {
		t.Fatal("DryRun() = false, want true")
	}
	if !got.Complete() {
		t.Fatal("Complete() = false, want true")
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

func TestOrphanConsolidationUsecase_TruncationDoesNotCutAgentText(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	sessionID := types.SessionID("sess-trunc-compose")
	// Agent text larger than half the mechanical budget must still appear in full.
	// No trailing whitespace: NewSessionRefinement trims summary for storage.
	agentSummary := "AGENT-ANCHOR:" + strings.Repeat("agent-reason-", 3000)
	existing, err := model.NewSessionRefinement(
		sessionID, 1, "evt-1", "evt-10", agentSummary, "",
		"agent", now.Add(-time.Hour), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Use the stored (trimmed) form for assertions against the composed output.
	agentSummary = existing.Summary()
	commands := make([]string, 0, 4000)
	for i := 0; i < 4000; i++ {
		commands = append(commands, strings.Repeat("x", 40))
	}
	orphan, err := model.NewSessionOrphanRange(
		sessionID,
		types.Some(types.EventID("evt-10")),
		"evt-20",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{orphan},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now,
			LastCreatedAt:  now,
			KindCounts:     map[string]int{"note": 1},
			Commands:       commands,
			EventCount:     1,
		},
	}
	refine := &refineStubForOrphan{current: types.Some(existing)}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})
	if _, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{StaleAfter: time.Hour}); err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	summary := refine.calls[0].Summary
	if !strings.Contains(summary, agentSummary) {
		t.Fatal("agent summary was truncated or altered; composition must preserve it wholesale")
	}
	if !strings.HasPrefix(summary, agentSummary) {
		t.Fatal("agent summary must remain the leading section")
	}
	if !strings.Contains(summary, "truncated") {
		t.Fatal("mechanical section should still declare its own truncation")
	}
}

func TestOrphanConsolidationUsecase_BoundedPassReturnsLimitAndHasMore(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	orphans := []*model.SessionOrphanRange{
		mustOrphanRange(t, "sess-1", "evt-1", now),
		mustOrphanRange(t, "sess-2", "evt-2", now),
		mustOrphanRange(t, "sess-3", "evt-3", now),
	}
	repo := &orphanRepoStub{
		candidates: orphans,
		hasMore:    true,
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if repo.lastLimit != 2 {
		t.Fatalf("DiscoverCandidates limit = %d, want 2", repo.lastLimit)
	}
	if diff := cmp.Diff(2, got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if !got.HasMore() {
		t.Fatal("HasMore() = false, want true")
	}
	if got.Complete() {
		t.Fatal("Complete() = true, want false when HasMore")
	}
	if len(refine.calls) != 2 {
		t.Fatalf("Refine calls = %d, want 2", len(refine.calls))
	}
}

func TestOrphanConsolidationUsecase_SingleFailureIsSkippedAndCounted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	ok1 := mustOrphanRange(t, "sess-ok-1", "evt-1", now)
	bad := mustOrphanRange(t, "sess-bad", "evt-bad", now)
	ok2 := mustOrphanRange(t, "sess-ok-2", "evt-2", now)
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{ok1, bad, ok2},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
		materialErrBySession: map[types.SessionID]error{
			"sess-bad": errOrphanMaterialStub,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v, want nil (single skip)", err)
	}
	if diff := cmp.Diff(3, got.Attempted()); diff != "" {
		t.Fatalf("Attempted mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, got.Skipped()); diff != "" {
		t.Fatalf("Skipped mismatch (-want +got):\n%s", diff)
	}
	if got.Complete() {
		t.Fatal("Complete() = true, want false when skipped > 0")
	}
	if len(refine.calls) != 2 {
		t.Fatalf("Refine calls = %d, want 2", len(refine.calls))
	}
}

func TestOrphanConsolidationUsecase_ThreeConsecutiveFailuresAbort(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	orphans := []*model.SessionOrphanRange{
		mustOrphanRange(t, "sess-a", "evt-a", now),
		mustOrphanRange(t, "sess-b", "evt-b", now),
		mustOrphanRange(t, "sess-c", "evt-c", now),
		mustOrphanRange(t, "sess-d", "evt-d", now),
	}
	repo := &orphanRepoStub{
		candidates: orphans,
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
	}
	refine := &refineStubForOrphan{err: errOrphanRefineStub}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	_, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err == nil {
		t.Fatal("Consolidate() error = nil, want consecutive failure abort")
	}
	if !strings.Contains(err.Error(), "3 consecutive failures") {
		t.Fatalf("error = %v, want consecutive failure message", err)
	}
}

func TestOrphanConsolidationUsecase_FailuresSeparatedBySuccessDoNotAbort(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	// fail, fail, success, fail, fail — never three consecutive without a success.
	orphans := []*model.SessionOrphanRange{
		mustOrphanRange(t, "sess-f1", "evt-1", now),
		mustOrphanRange(t, "sess-f2", "evt-2", now),
		mustOrphanRange(t, "sess-ok", "evt-ok", now),
		mustOrphanRange(t, "sess-f3", "evt-3", now),
		mustOrphanRange(t, "sess-f4", "evt-4", now),
	}
	repo := &orphanRepoStub{
		candidates: orphans,
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
		materialErrBySession: map[types.SessionID]error{
			"sess-f1": errOrphanMaterialStub,
			"sess-f2": errOrphanMaterialStub,
			"sess-f3": errOrphanMaterialStub,
			"sess-f4": errOrphanMaterialStub,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v, want nil (failures separated by success)", err)
	}
	if diff := cmp.Diff(1, got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(4, got.Skipped()); diff != "" {
		t.Fatalf("Skipped mismatch (-want +got):\n%s", diff)
	}
}

func TestOrphanConsolidationUsecase_BudgetExhaustionStopsWithHasMore(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	orphans := []*model.SessionOrphanRange{
		mustOrphanRange(t, "sess-1", "evt-1", now),
		mustOrphanRange(t, "sess-2", "evt-2", now),
		mustOrphanRange(t, "sess-3", "evt-3", now),
	}
	repo := &orphanRepoStub{
		candidates: orphans,
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
	}
	refine := &refineStubForOrphan{}
	// started = first Now(); each subsequent Now advances by step.
	// With step=1m and budget=90s: first budget check is 1m (< 90s) so one
	// candidate processes; second check is 2m (>= 90s) and the loop stops.
	clock := &steppingOrphanClock{at: now, step: time.Minute}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, clock)

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		Budget:     90 * time.Second,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v, want nil on budget exhaustion", err)
	}
	if !got.HasMore() {
		t.Fatal("HasMore() = false, want true after budget exhaustion")
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1 (only first candidate before budget)", got.ProducedCount())
	}
	if len(refine.calls) != 1 {
		t.Fatalf("Refine calls = %d, want 1", len(refine.calls))
	}
}

func TestOrphanConsolidationUsecase_CancelledContextReturnsError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	repo := &orphanRepoStub{
		candidates: []*model.SessionOrphanRange{
			mustOrphanRange(t, "sess-1", "evt-1", now),
		},
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sut.Consolidate(ctx, usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
	})
	if err == nil {
		t.Fatal("Consolidate() error = nil, want cancelled context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(refine.calls) != 0 {
		t.Fatalf("Refine calls = %d, want 0 when context already cancelled", len(refine.calls))
	}
}

func TestOrphanConsolidationUsecase_DryRunRespectsLimitAndSkipsLoadMaterialFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	orphans := []*model.SessionOrphanRange{
		mustOrphanRange(t, "sess-ok", "evt-ok", now),
		mustOrphanRange(t, "sess-bad", "evt-bad", now),
		mustOrphanRange(t, "sess-extra", "evt-extra", now),
	}
	repo := &orphanRepoStub{
		candidates: orphans,
		hasMore:    true,
		material: model.SessionOrphanMaterial{
			FirstCreatedAt: now, LastCreatedAt: now,
			KindCounts: map[string]int{"note": 1}, EventCount: 1,
		},
		materialErrBySession: map[types.SessionID]error{
			"sess-bad": errOrphanMaterialStub,
		},
	}
	refine := &refineStubForOrphan{}
	sut := usecase.NewOrphanConsolidationUsecase(repo, refine, fixedOrphanClock{at: now})

	got, err := sut.Consolidate(context.Background(), usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		DryRun:     true,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Consolidate(dry-run) error = %v, want nil (skip not abort)", err)
	}
	if repo.lastLimit != 2 {
		t.Fatalf("DiscoverCandidates limit = %d, want 2", repo.lastLimit)
	}
	if diff := cmp.Diff(1, got.ProducedCount()); diff != "" {
		t.Fatalf("ProducedCount mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, got.Skipped()); diff != "" {
		t.Fatalf("Skipped mismatch (-want +got):\n%s", diff)
	}
	if !got.HasMore() {
		t.Fatal("HasMore() = false, want true")
	}
	if len(refine.calls) != 0 {
		t.Fatalf("Refine calls = %d, want 0 on dry-run", len(refine.calls))
	}
	if repo.loadCalls != 2 {
		t.Fatalf("LoadMaterial calls = %d, want 2", repo.loadCalls)
	}
}
