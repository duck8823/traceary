package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

type orphanFixture struct {
	dbPath   string
	orphans  *sqlite.SessionOrphanRangeDatasource
	refine   *sqlite.SessionRefinementDatasource
	sessions *sqlite.SessionDatasource
	events   *sqlite.EventDatasource
}

func newOrphanFixture(t *testing.T) orphanFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return orphanFixture{
		dbPath:   dbPath,
		orphans:  sqlite.NewSessionOrphanRangeDatasource(database),
		refine:   sqlite.NewSessionRefinementDatasource(database),
		sessions: sqlite.NewSessionDatasource(database),
		events:   sqlite.NewEventDatasource(database),
	}
}

func seedOrphanSession(
	ctx context.Context,
	t *testing.T,
	fx orphanFixture,
	sessionIDValue string,
	seeds []eventSeed,
	ended bool,
) types.SessionID {
	t.Helper()
	sessionID := types.SessionID(sessionIDValue)
	started := seeds[0].at
	session := model.NewSession(sessionID, started, "cli", "codex", "ws")
	boundary, err := model.NewEventWithClock(
		types.EventID(sessionIDValue+"-start"),
		types.EventKindSessionStarted,
		"cli", "codex", sessionID, "ws", "session started",
		fixedEventClock{at: started.Add(-time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		event, err := model.NewEventWithClock(
			types.EventID(seed.id),
			types.EventKindNote,
			"cli", "codex", sessionID, "ws", "body "+seed.id,
			fixedEventClock{at: seed.at},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.events.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if ended {
		got, err := fx.sessions.FindByID(ctx, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		sess, ok := got.Value()
		if !ok {
			t.Fatal("session missing after seed")
		}
		endAt := seeds[len(seeds)-1].at.Add(time.Minute)
		if err := sess.End(endAt, "done"); err != nil {
			t.Fatal(err)
		}
		endEvent, err := model.NewEventWithClock(
			types.EventID(sessionIDValue+"-end"),
			types.EventKindSessionEnded,
			"cli", "codex", sessionID, "ws", "session ended",
			fixedEventClock{at: endAt},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.sessions.SaveBoundary(ctx, sess, endEvent); err != nil {
			t.Fatal(err)
		}
	}
	return sessionID
}

func TestSessionOrphanRangeDatasource_RecordAtCompactAfterUnfoldedRange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	// Fractional-second pair so plain TEXT ordering would invert (#1185).
	whole := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 1, 10, 0, 0, 500_000_000, time.UTC)
	compactAt := time.Date(2026, 8, 1, 10, 0, 1, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-compact", []eventSeed{
		{id: "evt-whole", at: whole},
		{id: "evt-frac", at: frac},
		{id: "evt-compact", at: compactAt},
	}, false)

	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "to whole", ProducedBy: "agent", CoversTo: "evt-whole",
	}); err != nil {
		t.Fatalf("Refine() error = %v", err)
	}

	orphanUC := usecase.NewSessionOrphanRangeUsecase(fx.orphans, fx.refine, fx.events, types.SystemClock{})
	if err := orphanUC.RecordAtCompact(ctx, sessionID, "evt-compact"); err != nil {
		t.Fatalf("RecordAtCompact() error = %v", err)
	}

	candidates, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, compactAt, 100)
	if err != nil {
		t.Fatalf("DiscoverCandidates() error = %v", err)
	}
	if len(candidates.Ranges) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates.Ranges))
	}
	if candidates.HasMore {
		t.Fatal("HasMore = true, want false")
	}
	got := candidates.Ranges[0]
	if got.ToEventID() != "evt-compact" {
		t.Fatalf("ToEventID = %s, want evt-compact", got.ToEventID())
	}
	from, ok := got.FromEventID().Value()
	if !ok || from != "evt-whole" {
		t.Fatalf("FromEventID = %v present=%v, want evt-whole", from, ok)
	}

	// Material must include evt-frac under ts_norm order (not lexical).
	material, err := fx.orphans.LoadMaterial(ctx, sessionID, types.Some(types.EventID("evt-whole")), "evt-compact")
	if err != nil {
		t.Fatalf("LoadMaterial() error = %v", err)
	}
	if material.EventCount != 2 { // evt-frac + evt-compact
		t.Fatalf("EventCount = %d, want 2 (frac+compact after exclusive whole)", material.EventCount)
	}
}

func TestSessionOrphanRangeDatasource_NoOrphanWhenDigestCoversCompact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-covered", []eventSeed{
		{id: "evt-1", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{id: "evt-compact", at: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)},
	}, false)

	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "digest", ProducedBy: "hook:post-compact:claude", CoversTo: "evt-compact",
	}); err != nil {
		t.Fatalf("Refine() error = %v", err)
	}

	orphanUC := usecase.NewSessionOrphanRangeUsecase(fx.orphans, fx.refine, fx.events, types.SystemClock{})
	if err := orphanUC.RecordAtCompact(ctx, sessionID, "evt-compact"); err != nil {
		t.Fatalf("RecordAtCompact() error = %v", err)
	}
	if count := countOrphanRangesAt(t, fx.dbPath); count != 0 {
		t.Fatalf("orphan rows = %d, want 0", count)
	}
}

func TestSessionOrphanRangeDatasource_GCFindsEndedSessionWithoutMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	// Fractional-second ordering regression guard.
	whole := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 1, 10, 0, 0, 500_000_000, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-ended", []eventSeed{
		{id: "evt-whole", at: whole},
		{id: "evt-frac", at: frac},
	}, true)

	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "to whole", ProducedBy: "agent", CoversTo: "evt-whole",
	}); err != nil {
		t.Fatalf("Refine() error = %v", err)
	}

	// No orphan row recorded — discovery must still find the gap past covers_to.
	now := frac.Add(2 * time.Hour)
	candidates, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, 100)
	if err != nil {
		t.Fatalf("DiscoverCandidates() error = %v", err)
	}
	if len(candidates.Ranges) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates.Ranges))
	}
	got := candidates.Ranges[0]
	// Latest event is the session_ended boundary.
	if got.ToEventID() != "sess-ended-end" {
		t.Fatalf("ToEventID = %s, want sess-ended-end", got.ToEventID())
	}
	from, ok := got.FromEventID().Value()
	if !ok || from != "evt-whole" {
		t.Fatalf("FromEventID = %v present=%v, want evt-whole", from, ok)
	}
	if got.SessionID() != sessionID {
		t.Fatalf("SessionID = %s", got.SessionID())
	}
}

func TestSessionOrphanRangeDatasource_GCProducesDegradedAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-gc", []eventSeed{
		{id: "evt-1", at: base},
		{id: "evt-2", at: base.Add(time.Hour)},
	}, true)

	now := base.Add(48 * time.Hour)
	consol := usecase.NewOrphanConsolidationUsecase(
		fx.orphans,
		usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{}),
		fixedEventClock{at: now},
	)
	first, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{StaleAfter: 24 * time.Hour})
	if err != nil {
		t.Fatalf("first Consolidate() error = %v", err)
	}
	if first.ProducedCount() != 1 {
		t.Fatalf("first ProducedCount = %d, want 1", first.ProducedCount())
	}

	got, err := fx.refine.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := got.Value()
	if !ok {
		t.Fatal("expected degraded refinement")
	}
	if !row.Degraded() {
		t.Fatal("Degraded() = false, want true")
	}
	if row.ProducedBy() != "gc:orphan-consolidation" {
		t.Fatalf("ProducedBy = %q", row.ProducedBy())
	}
	if !strings.Contains(row.Summary(), "Mechanical summary") {
		t.Fatalf("summary missing mechanical header: %q", row.Summary())
	}
	if !strings.Contains(row.Summary(), "does not recover agent reasoning") {
		t.Fatalf("summary must state reasoning is gone: %q", row.Summary())
	}
	if strings.Contains(strings.ToLower(row.Summary()), "nothing is lost") {
		t.Fatalf("summary must not claim nothing is lost: %q", row.Summary())
	}

	second, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{StaleAfter: 24 * time.Hour})
	if err != nil {
		t.Fatalf("second Consolidate() error = %v", err)
	}
	if second.ProducedCount() != 0 {
		t.Fatalf("second ProducedCount = %d, want 0 (already covered)", second.ProducedCount())
	}
	if count := countSessionRefinementsAt(t, fx.dbPath); count != 1 {
		t.Fatalf("refinements = %d, want 1", count)
	}
}

func TestSessionOrphanRangeDatasource_RunningSessionLeftAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	_ = seedOrphanSession(ctx, t, fx, "sess-live", []eventSeed{
		{id: "evt-1", at: now.Add(-time.Hour)},
		{id: "evt-2", at: now.Add(-time.Minute)},
	}, false)

	candidates, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, 100)
	if err != nil {
		t.Fatalf("DiscoverCandidates() error = %v", err)
	}
	if len(candidates.Ranges) != 0 {
		t.Fatalf("candidates = %d, want 0 for live non-stale session", len(candidates.Ranges))
	}
}

func TestSessionOrphanRangeDatasource_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	_ = seedOrphanSession(ctx, t, fx, "sess-dry", []eventSeed{
		{id: "evt-1", at: base},
	}, true)

	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, types.SystemClock{})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Consolidate(dry-run) error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}
	if count := countSessionRefinementsAt(t, fx.dbPath); count != 0 {
		t.Fatalf("refinements written on dry-run = %d, want 0", count)
	}
}

// Dry-run is promised to use a read-only connection. A store whose file is
// 0400 must still count candidates; open() would fail by setting journal mode.
func TestSessionOrphanRangeDatasource_DryRunSucceedsOnReadOnlyStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	_ = seedOrphanSession(ctx, t, fx, "sess-ro-dry", []eventSeed{
		{id: "evt-1", at: base},
	}, true)

	if err := os.Chmod(fx.dbPath, 0o400); err != nil {
		t.Fatalf("Chmod(read-only) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(fx.dbPath, 0o600); err != nil {
			t.Errorf("Chmod(restore) error = %v", err)
		}
	})

	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, types.SystemClock{})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Consolidate(dry-run) on read-only store error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}
}

func TestSessionOrphanRangeDatasource_ComposesOntoAgentAuthoredRefinement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-compose", []eventSeed{
		{id: "evt-agent-end", at: base},
		{id: "evt-orphan-a", at: base.Add(time.Hour)},
		{id: "evt-orphan-b", at: base.Add(2 * time.Hour)},
	}, true)

	const agentSummary = "Agent folded early work: split the shared types package before the UI rewrite."
	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := refineUC.Refine(ctx, usecase.SessionRefineInput{
		SessionID:  sessionID,
		Summary:    agentSummary,
		Keywords:   "types,split",
		ProducedBy: "agent",
		CoversTo:   "evt-agent-end",
		Degraded:   false,
	}); err != nil {
		t.Fatalf("Refine(agent) error = %v", err)
	}

	now := base.Add(48 * time.Hour)
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, fixedEventClock{at: now})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{StaleAfter: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}

	stored, err := fx.refine.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := stored.Value()
	if !ok {
		t.Fatal("expected composed refinement")
	}

	// Coverage spans the original agent range and the orphan tail.
	if row.CoversFromEventID() != "sess-compose-start" {
		t.Fatalf("CoversFrom = %s, want session start boundary", row.CoversFromEventID())
	}
	if row.CoversToEventID() != "sess-compose-end" {
		t.Fatalf("CoversTo = %s, want session end (latest event)", row.CoversToEventID())
	}
	// Text and coverage agree: agent prose for the early range, mechanical for the tail.
	if !strings.Contains(row.Summary(), agentSummary) {
		t.Fatalf("stored summary lost agent text: %q", row.Summary())
	}
	if !strings.HasPrefix(row.Summary(), agentSummary) {
		t.Fatalf("agent text must lead composed summary: %q", row.Summary())
	}
	if !strings.Contains(row.Summary(), "degraded section") {
		t.Fatalf("stored summary missing degraded section label: %q", row.Summary())
	}
	if !strings.Contains(row.Summary(), "after evt-agent-end through sess-compose-end") {
		t.Fatalf("stored summary missing orphan range: %q", row.Summary())
	}
	if !strings.Contains(row.Summary(), "Mechanical summary") {
		t.Fatalf("stored summary missing mechanical body: %q", row.Summary())
	}
	// Pin degraded=true: mixed authorship is not purely agent-written.
	if !row.Degraded() {
		t.Fatal("Degraded() = false on composed row, want true")
	}
	if row.Keywords() != "types,split" {
		t.Fatalf("Keywords = %q, want preserved agent keywords", row.Keywords())
	}
	if row.ProducedBy() != "gc:orphan-consolidation" {
		t.Fatalf("ProducedBy = %q", row.ProducedBy())
	}
	if row.Generation() != 2 {
		t.Fatalf("Generation = %d, want 2 (supersede)", row.Generation())
	}
	if !row.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = false after compose, want true")
	}
}

func TestSessionOrphanRangeDatasource_FoldThenEndThenReduceStaysWakeEligible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-fold-end", []eventSeed{
		{id: "evt-folded", at: base},
	}, true)

	const agentSummary = "Motivation: verify the record pillar. Change: folded twelve events."
	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := refineUC.Refine(ctx, usecase.SessionRefineInput{
		SessionID:         sessionID,
		Summary:           agentSummary,
		ProducedBy:        "agent",
		CoversTo:          "evt-folded",
		Degraded:          false,
		HasAgentReasoning: true,
	}); err != nil {
		t.Fatalf("Refine(agent) error = %v", err)
	}

	now := base.Add(48 * time.Hour)
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, fixedEventClock{at: now})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{StaleAfter: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1 (coverage-only advance over session_ended)", got.ProducedCount())
	}

	stored, err := fx.refine.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := stored.Value()
	if !ok {
		t.Fatal("expected refinement after reduce")
	}
	if row.Summary() != agentSummary {
		t.Fatalf("Summary() = %q, want unchanged agent text", row.Summary())
	}
	if strings.Contains(row.Summary(), "Mechanical summary") {
		t.Fatalf("lifecycle-only reduce must not attach a mechanical footnote: %q", row.Summary())
	}
	if row.Degraded() {
		t.Fatal("Degraded() = true after fold→end→reduce, want false")
	}
	if !row.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = false after fold→end→reduce, want true")
	}
	if row.CoversToEventID() != "sess-fold-end-end" {
		t.Fatalf("CoversTo = %s, want session_ended", row.CoversToEventID())
	}

	wake := sqlite.NewSessionWakeSummaryDatasource(sqlite.NewDatabase(fx.dbPath, onDiskSQLiteMigrations(t)))
	eligible, err := wake.ListEligible(ctx, "ws", "sess-waking", 64)
	if err != nil {
		t.Fatalf("ListEligible() error = %v", err)
	}
	found := false
	for _, item := range eligible {
		if item.SessionID == sessionID {
			found = true
			if item.Summary != agentSummary {
				t.Fatalf("wake summary = %q, want agent text", item.Summary)
			}
		}
	}
	if !found {
		t.Fatal("folded session missing from ListEligible after fold→end→reduce")
	}
}

func TestSessionOrphanRangeDatasource_MechanicalOnlyStaysWakeIneligible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-mech-only", []eventSeed{
		{id: "evt-note", at: base},
	}, true)

	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, fixedEventClock{at: base.Add(48 * time.Hour)})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{StaleAfter: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}

	stored, err := fx.refine.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := stored.Value()
	if !ok {
		t.Fatal("expected mechanical refinement")
	}
	if !row.Degraded() {
		t.Fatal("Degraded() = false on mechanical-only row, want true")
	}
	if row.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = true on mechanical-only row, want false")
	}

	wake := sqlite.NewSessionWakeSummaryDatasource(sqlite.NewDatabase(fx.dbPath, onDiskSQLiteMigrations(t)))
	eligible, err := wake.ListEligible(ctx, "ws", "sess-waking", 64)
	if err != nil {
		t.Fatalf("ListEligible() error = %v", err)
	}
	for _, item := range eligible {
		if item.SessionID == sessionID {
			t.Fatal("mechanical-only session must not appear in ListEligible")
		}
	}
}

func TestSessionOrphanRangeDatasource_AgentFoldAfterMechanicalBecomesWakeEligible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-upgrade", []eventSeed{
		{id: "evt-mech", at: base},
		{id: "evt-agent", at: base.Add(time.Hour)},
	}, true)

	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := refineUC.Refine(ctx, usecase.SessionRefineInput{
		SessionID:  sessionID,
		Summary:    "Mechanical summary (degraded=1).",
		ProducedBy: "gc:orphan-consolidation",
		CoversTo:   "evt-mech",
		Degraded:   true,
	}); err != nil {
		t.Fatalf("Refine(mechanical) error = %v", err)
	}
	const agentSummary = "Remember the failing shard and the flake owner."
	if _, err := refineUC.Refine(ctx, usecase.SessionRefineInput{
		SessionID:  sessionID,
		Summary:    agentSummary,
		ProducedBy: "hook:post-compact:claude",
		CoversTo:   "evt-agent",
		Degraded:   false,
	}); err != nil {
		t.Fatalf("Refine(agent after mechanical) error = %v", err)
	}

	stored, err := fx.refine.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := stored.Value()
	if !ok {
		t.Fatal("expected upgraded refinement")
	}
	if row.Degraded() {
		t.Fatal("Degraded() = true after agent fold, want false")
	}
	if !row.HasAgentReasoning() {
		t.Fatal("HasAgentReasoning() = false after agent fold over mechanical-only, want true")
	}

	wake := sqlite.NewSessionWakeSummaryDatasource(sqlite.NewDatabase(fx.dbPath, onDiskSQLiteMigrations(t)))
	eligible, err := wake.ListEligible(ctx, "ws", "sess-waking", 64)
	if err != nil {
		t.Fatalf("ListEligible() error = %v", err)
	}
	found := false
	for _, item := range eligible {
		if item.SessionID == sessionID {
			found = true
			if item.Summary != agentSummary {
				t.Fatalf("wake summary = %q, want agent fold", item.Summary)
			}
		}
	}
	if !found {
		t.Fatal("agent fold after mechanical-only must appear in ListEligible")
	}
}

func TestSessionOrphanRangeDatasource_DiscoverCandidatesOnPreMigration47Store(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Store schema stops at migration 46: session_orphan_ranges does not exist.
	// Discovery must still succeed via marker-free path (sessions/events/refinements).
	dbPath := filepath.Join(t.TempDir(), "traceary-pre47.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrationsBefore(t, 47))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-47) error = %v", err)
	}
	// Confirm the marker table is absent — this is the regression surface.
	if tableExistsAt(t, dbPath, "session_orphan_ranges") {
		t.Fatal("session_orphan_ranges must not exist before migration 47")
	}

	fx := orphanFixture{
		dbPath:   dbPath,
		orphans:  sqlite.NewSessionOrphanRangeDatasource(database),
		refine:   sqlite.NewSessionRefinementDatasource(database),
		sessions: sqlite.NewSessionDatasource(database),
		events:   sqlite.NewEventDatasource(database),
	}
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-pre47", []eventSeed{
		{id: "evt-1", at: base},
		{id: "evt-2", at: base.Add(time.Hour)},
	}, true)

	// Schema 46 has session_refinements without has_agent_reasoning (#1877
	// adds that in 60). Seed with the 46 column list; Refine() would fail
	// because the current write path selects the new column.
	raw, err := sql.Open("sqlite", fx.dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := raw.Exec(`
		INSERT INTO session_refinements(
			session_id, generation, covers_from_event_id, covers_to_event_id,
			summary, keywords, produced_by, produced_at, degraded
		) VALUES (?, 1, ?, ?, 'to evt-1', '', 'agent', ?, 0)`,
		sessionID.String(), sessionID.String()+"-start", "evt-1",
		base.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("INSERT pre-47 refinement error = %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close pre-47 seed db error = %v", err)
	}
	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})

	now := base.Add(48 * time.Hour)
	candidates, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, 100)
	if err != nil {
		t.Fatalf("DiscoverCandidates() on pre-47 store error = %v", err)
	}
	if len(candidates.Ranges) != 1 {
		t.Fatalf("candidates = %d, want 1 from marker-free discovery", len(candidates.Ranges))
	}

	// Full consolidation path (what gc --dry-run hits first) must also succeed.
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, fixedEventClock{at: now})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Consolidate(dry-run) on pre-47 store error = %v", err)
	}
	if got.ProducedCount() != 1 {
		t.Fatalf("ProducedCount = %d, want 1", got.ProducedCount())
	}
}

func TestSessionOrphanRangeDatasource_LoadMaterialIncludesCommandsAndKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sessionID := types.SessionID("sess-mat")
	session := model.NewSession(sessionID, base, "cli", "codex", "ws")
	start, err := model.NewEventWithClock(
		"sess-mat-start", types.EventKindSessionStarted, "cli", "codex", sessionID, "ws", "start",
		fixedEventClock{at: base.Add(-time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	prompt, err := model.NewEventWithClock(
		"evt-p", types.EventKindPrompt, "cli", "codex", sessionID, "ws", "please run tests",
		fixedEventClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.events.Save(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	cmdEvt, err := model.NewEventWithClock(
		"evt-c", types.EventKindCommandExecuted, "cli", "codex", sessionID, "ws", "ran",
		fixedEventClock{at: base.Add(time.Minute)},
	)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := model.NewCommandAudit("evt-c", "go test ./...", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.events.SaveWithAudit(ctx, cmdEvt, audit); err != nil {
		t.Fatal(err)
	}

	gotSess, err := fx.sessions.FindByID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := gotSess.Value()
	endAt := base.Add(2 * time.Hour)
	if err := sess.End(endAt, "done"); err != nil {
		t.Fatal(err)
	}
	endEvt, err := model.NewEventWithClock(
		"sess-mat-end", types.EventKindSessionEnded, "cli", "codex", sessionID, "ws", "end",
		fixedEventClock{at: endAt},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, sess, endEvt); err != nil {
		t.Fatal(err)
	}

	material, err := fx.orphans.LoadMaterial(ctx, sessionID, types.None[types.EventID](), "sess-mat-end")
	if err != nil {
		t.Fatalf("LoadMaterial() error = %v", err)
	}
	if diff := cmp.Diff(map[string]int{
		"session_started":  1,
		"prompt":           1,
		"command_executed": 1,
		"session_ended":    1,
	}, material.KindCounts); diff != "" {
		t.Fatalf("KindCounts mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"go test ./..."}, material.Commands); diff != "" {
		t.Fatalf("Commands mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionOrphanRangeDatasource_FractionalSecondBoundaryOrdering(t *testing.T) {
	t.Parallel()

	// Same hazard as FractionalSecondBoundaryOrdering on refinements (#1185):
	// variable-width RFC3339Nano is not lexically ordered. Discovery and
	// material loading must use ts_norm.
	ctx := context.Background()
	fx := newOrphanFixture(t)
	whole := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 1, 10, 0, 0, 500_000_000, time.UTC)
	sessionID := seedOrphanSession(ctx, t, fx, "sess-frac-orphan", []eventSeed{
		{id: "evt-whole", at: whole},
		{id: "evt-frac", at: frac},
	}, true)

	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	if _, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "to whole", ProducedBy: "agent", CoversTo: "evt-whole",
	}); err != nil {
		t.Fatalf("Refine() error = %v", err)
	}

	after, err := fx.events.EventIsStrictlyAfter(ctx, "evt-frac", "evt-whole")
	if err != nil {
		t.Fatalf("EventIsStrictlyAfter() error = %v", err)
	}
	if !after {
		t.Fatal("evt-frac should be strictly after evt-whole under ts_norm")
	}

	candidates, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, frac.Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("DiscoverCandidates() error = %v", err)
	}
	if len(candidates.Ranges) != 1 {
		t.Fatalf("candidates = %d, want 1 (gap after covers_to under ts_norm)", len(candidates.Ranges))
	}
	from, ok := candidates.Ranges[0].FromEventID().Value()
	if !ok || from != "evt-whole" {
		t.Fatalf("FromEventID = %v present=%v", from, ok)
	}
}

func TestSessionOrphanRangeDatasource_DiscoverCandidatesRespectsLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("sess-limit-%d", i)
		_ = seedOrphanSession(ctx, t, fx, id, []eventSeed{
			{id: id + "-evt", at: base.Add(time.Duration(i) * time.Minute)},
		}, true)
	}
	now := base.Add(48 * time.Hour)
	got, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, 2)
	if err != nil {
		t.Fatalf("DiscoverCandidates() error = %v", err)
	}
	if len(got.Ranges) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got.Ranges))
	}
	if !got.HasMore {
		t.Fatal("HasMore = false, want true when more candidates than limit")
	}
}

func TestSessionOrphanRangeDatasource_DiscoverCandidatesProgressesAcrossPages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	// Six ended sessions without refinements: first page limit=3, second page the rest.
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("sess-page-%02d", i)
		_ = seedOrphanSession(ctx, t, fx, id, []eventSeed{
			{id: id + "-evt", at: base.Add(time.Duration(i) * time.Minute)},
		}, true)
	}
	now := base.Add(48 * time.Hour)
	limit := 3

	first, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, limit)
	if err != nil {
		t.Fatalf("first DiscoverCandidates() error = %v", err)
	}
	if len(first.Ranges) != limit {
		t.Fatalf("first page = %d, want %d", len(first.Ranges), limit)
	}
	if !first.HasMore {
		t.Fatal("first HasMore = false, want true")
	}

	// Consolidate the first page so those sessions leave the candidate set.
	refineUC := usecase.NewSessionRefinementUsecase(fx.sessions, fx.refine, fx.events, types.SystemClock{})
	consol := usecase.NewOrphanConsolidationUsecase(fx.orphans, refineUC, fixedEventClock{at: now})
	got, err := consol.Consolidate(ctx, usecase.OrphanConsolidationInput{
		StaleAfter: 24 * time.Hour,
		Limit:      limit,
	})
	if err != nil {
		t.Fatalf("Consolidate() error = %v", err)
	}
	if got.ProducedCount() != limit {
		t.Fatalf("ProducedCount = %d, want %d", got.ProducedCount(), limit)
	}

	second, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, limit)
	if err != nil {
		t.Fatalf("second DiscoverCandidates() error = %v", err)
	}
	if len(second.Ranges) != limit {
		t.Fatalf("second page = %d, want %d (remaining candidates)", len(second.Ranges), limit)
	}
	// Second page must return different sessions, not the same already-folded ones.
	firstSet := map[string]struct{}{}
	for _, r := range first.Ranges {
		firstSet[r.SessionID().String()] = struct{}{}
	}
	for _, r := range second.Ranges {
		if _, dup := firstSet[r.SessionID().String()]; dup {
			t.Fatalf("second page re-returned session %s already on first page", r.SessionID())
		}
	}
	if second.HasMore {
		t.Fatal("second HasMore = true, want false after consolidating first page of 6 with limit 3")
	}
}

func TestSessionOrphanRangeDatasource_SessionsWithNoEventsAreNotReturned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	// Session with events (should appear).
	_ = seedOrphanSession(ctx, t, fx, "sess-with-events", []eventSeed{
		{id: "evt-present", at: base},
	}, true)

	// Session row with no events: create via SaveBoundary start only then end without body events.
	// seedOrphanSession always creates events; insert a bare ended session via SQL-level path.
	emptyID := types.SessionID("sess-no-events")
	session := model.NewSession(emptyID, base, "cli", "codex", "ws")
	// Boundary start event is itself an event — remove it after end to get a session with zero events.
	start, err := model.NewEventWithClock(
		"sess-no-events-start", types.EventKindSessionStarted, "cli", "codex", emptyID, "ws", "start",
		fixedEventClock{at: base},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, start); err != nil {
		t.Fatal(err)
	}
	gotSess, err := fx.sessions.FindByID(ctx, emptyID)
	if err != nil {
		t.Fatal(err)
	}
	sess, ok := gotSess.Value()
	if !ok {
		t.Fatal("empty session missing")
	}
	endAt := base.Add(time.Hour)
	if err := sess.End(endAt, "done"); err != nil {
		t.Fatal(err)
	}
	endEvt, err := model.NewEventWithClock(
		"sess-no-events-end", types.EventKindSessionEnded, "cli", "codex", emptyID, "ws", "end",
		fixedEventClock{at: endAt},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, sess, endEvt); err != nil {
		t.Fatal(err)
	}
	// Delete all events for the empty session so it has no event rows.
	db, err := sql.Open("sqlite", fx.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`DELETE FROM events WHERE session_id = ?`, emptyID.String()); err != nil {
		t.Fatalf("DELETE events for empty session: %v", err)
	}

	now := base.Add(48 * time.Hour)
	candidates, err := fx.orphans.DiscoverCandidates(ctx, 24*time.Hour, now, 100)
	if err != nil {
		t.Fatalf("DiscoverCandidates() error = %v", err)
	}
	for _, r := range candidates.Ranges {
		if r.SessionID() == emptyID {
			t.Fatal("session with no events must not be a candidate")
		}
	}
	if len(candidates.Ranges) != 1 {
		t.Fatalf("candidates = %d, want 1 (only session with events)", len(candidates.Ranges))
	}
}

func countOrphanRangesAt(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_orphan_ranges`).Scan(&count); err != nil {
		t.Fatalf("COUNT session_orphan_ranges error = %v", err)
	}
	return count
}

func tableExistsAt(t *testing.T, dbPath, table string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var exists int
	if err := db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`,
		table,
	).Scan(&exists); err != nil {
		t.Fatalf("probe table %s: %v", table, err)
	}
	return exists != 0
}

// TestSessionOrphanRangeDatasource_DiscoveryPlanUsesNormalizedTimestampIndex
// pins the reason the discovery query reads events.created_at_norm instead of
// calling ts_norm(created_at). Discovery evaluates the latest-event subquery for
// every session it scans, so an unindexable ordering there costs a sort per
// session — exactly the open-ended work #1719 exists to bound.
//
// Measured on the same fixture: with ts_norm(e2.created_at) the plan ends in
// "USE TEMP B-TREE FOR ORDER BY" and the activity probe degrades to
// "SEARCH e USING COVERING INDEX idx_events_session_created_at (session_id=?)",
// dropping the timestamp from the seek.
func TestSessionOrphanRangeDatasource_DiscoveryPlanUsesNormalizedTimestampIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)

	query, err := os.ReadFile(filepath.Join("sql", "list_ended_or_stale_sessions_for_orphan.sql"))
	if err != nil {
		t.Fatalf("read discovery query: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+fx.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = db.Close() }()

	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	rows, err := db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+strings.TrimSpace(string(query)),
		"", cutoff, cutoff, 10,
	)
	if err != nil {
		t.Fatalf("explain discovery query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}
	if len(plan) == 0 {
		t.Fatal("query plan is empty")
	}

	joined := strings.Join(plan, "\n")
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Errorf("query plan sorts instead of using an index:\n%s", joined)
	}
	if !strings.Contains(joined, "idx_events_session_created_at_norm_id_desc") {
		t.Errorf("query plan does not use the normalized timestamp index:\n%s", joined)
	}
}

// TestSessionOrphanRangeDatasource_InsertedEventsCarryNormalizedTimestamp pins
// the invariant the discovery query depends on. Discovery picks a session's
// latest event with ORDER BY created_at_norm DESC, while Go re-checks coverage
// with ts_norm(created_at). If an inserted row could leave created_at_norm NULL
// or disagreeing, SQLite would sort it last, discovery would read an older
// event as the latest, and the session would be dropped from the candidate set
// — after which the pass reports no remaining candidates and deletion removes
// events that were never folded.
//
// migrate_test covers the migration-031 backfill. This covers the trigger path
// that every later insert takes.
func TestSessionOrphanRangeDatasource_InsertedEventsCarryNormalizedTimestamp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seedOrphanSession(ctx, t, fx, "sess-norm", []eventSeed{
		// Whole second and sub-second forms render at different widths under
		// RFC3339Nano, which is the shape that breaks lexical ordering (#1185).
		{id: "evt-whole", at: base},
		{id: "evt-frac", at: base.Add(500 * time.Millisecond)},
		{id: "evt-nano", at: base.Add(time.Second + 1)},
	}, true)

	db, err := sql.Open("sqlite", "file:"+fx.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(ctx, `
SELECT id,
       created_at_norm IS NULL AS is_null,
       COALESCE(created_at_norm, '') AS stored,
       COALESCE(ts_norm(created_at), '') AS computed
  FROM events
 WHERE session_id = ?
`, "sess-norm")
	if err != nil {
		t.Fatalf("read normalized timestamps: %v", err)
	}
	defer func() { _ = rows.Close() }()

	seen := 0
	for rows.Next() {
		var id, stored, computed string
		var isNull bool
		if err := rows.Scan(&id, &isNull, &stored, &computed); err != nil {
			t.Fatalf("scan normalized timestamp: %v", err)
		}
		seen++
		if isNull {
			t.Errorf("event %s has NULL created_at_norm; discovery would sort it last and miss it", id)
			continue
		}
		if diff := cmp.Diff(computed, stored); diff != "" {
			t.Errorf("event %s created_at_norm differs from ts_norm(created_at) (-want +got):\n%s", id, diff)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate normalized timestamps: %v", err)
	}
	if seen == 0 {
		t.Fatal("no events found for the seeded session")
	}
}
