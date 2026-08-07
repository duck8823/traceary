package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestSessionRefinementDatasource_WriteTwiceOverlappingRangesYieldsGeneration2(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-overlap", []eventSeed{
		{id: "evt-a", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{id: "evt-b", at: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)},
		{id: "evt-c", at: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
	})
	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.sut, fx.events, types.SystemClock{})

	first, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "first half", ProducedBy: "agent", CoversTo: "evt-b",
	})
	if err != nil {
		t.Fatalf("first Refine() error = %v", err)
	}
	if first.Outcome() != model.SessionRefineOutcomeCreated || first.Refinement().Generation() != 1 {
		t.Fatalf("first result = %s gen=%d", first.Outcome(), first.Refinement().Generation())
	}

	second, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "full session", ProducedBy: "agent", CoversTo: "evt-c",
	})
	if err != nil {
		t.Fatalf("second Refine() error = %v", err)
	}
	if second.Outcome() != model.SessionRefineOutcomeSuperseded {
		t.Fatalf("second Outcome() = %q, want superseded", second.Outcome())
	}
	row := second.Refinement()
	if row.Generation() != 2 {
		t.Fatalf("Generation() = %d, want 2", row.Generation())
	}
	// covers_from is the session's earliest event (the start boundary), not the
	// first note — re-summarisation keeps that lower bound forever.
	if row.CoversFromEventID() != "sess-overlap-start" || row.CoversToEventID() != "evt-c" {
		t.Fatalf("coverage = %s..%s, want sess-overlap-start..evt-c", row.CoversFromEventID(), row.CoversToEventID())
	}
	if row.Summary() != "full session" {
		t.Fatalf("Summary() = %q", row.Summary())
	}
	if count := countSessionRefinementsAt(t, fx.dbPath); count != 1 {
		t.Fatalf("session_refinements rows = %d, want 1", count)
	}
}

func TestSessionRefinementDatasource_SameRangeTwiceIsNoOp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-noop", []eventSeed{
		{id: "evt-1", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{id: "evt-2", at: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)},
	})
	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.sut, fx.events, types.SystemClock{})

	first, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "stable text", ProducedBy: "agent", CoversTo: "evt-2",
	})
	if err != nil {
		t.Fatalf("first Refine() error = %v", err)
	}
	second, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "should not overwrite", ProducedBy: "other", CoversTo: "evt-2",
	})
	if err != nil {
		t.Fatalf("second Refine() error = %v", err)
	}
	if second.Outcome() != model.SessionRefineOutcomeUnchanged {
		t.Fatalf("Outcome() = %q, want unchanged", second.Outcome())
	}
	if second.Refinement().Generation() != first.Refinement().Generation() {
		t.Fatalf("generation changed: first=%d second=%d", first.Refinement().Generation(), second.Refinement().Generation())
	}
	if second.Refinement().Summary() != "stable text" {
		t.Fatalf("Summary() = %q, want stable text", second.Refinement().Summary())
	}
	if count := countSessionRefinementsAt(t, fx.dbPath); count != 1 {
		t.Fatalf("session_refinements rows = %d, want 1", count)
	}
}

func TestSessionRefinementDatasource_OlderCoversToIsNoOp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-downgrade", []eventSeed{
		{id: "evt-1", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{id: "evt-2", at: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)},
		{id: "evt-3", at: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
	})
	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.sut, fx.events, types.SystemClock{})
	if _, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "to evt-3", ProducedBy: "agent", CoversTo: "evt-3",
	}); err != nil {
		t.Fatalf("seed Refine() error = %v", err)
	}
	got, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "downgrade", ProducedBy: "agent", CoversTo: "evt-2",
	})
	if err != nil {
		t.Fatalf("Refine(older) error = %v", err)
	}
	if got.Outcome() != model.SessionRefineOutcomeUnchanged {
		t.Fatalf("Outcome() = %q, want unchanged", got.Outcome())
	}
	if got.Refinement().CoversToEventID() != "evt-3" || got.Refinement().Summary() != "to evt-3" {
		t.Fatalf("row was downgraded: to=%s summary=%q", got.Refinement().CoversToEventID(), got.Refinement().Summary())
	}
}

func TestSessionRefinementDatasource_UnknownSessionAndForeignEventAreErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	_ = seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-a", []eventSeed{
		{id: "evt-a1", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
	})
	_ = seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-b", []eventSeed{
		{id: "evt-b1", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
	})
	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.sut, fx.events, types.SystemClock{})

	_, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: "missing", Summary: "x", ProducedBy: "agent", CoversTo: "evt-a1",
	})
	if err == nil {
		t.Fatal("unknown session: error = nil, want error")
	}

	_, err = uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: "sess-a", Summary: "x", ProducedBy: "agent", CoversTo: "evt-b1",
	})
	if err == nil {
		t.Fatal("foreign event: error = nil, want error")
	}
}

func TestSessionRefinementDatasource_FractionalSecondBoundaryOrdering(t *testing.T) {
	t.Parallel()

	// #1185 hazard: variable-width RFC3339Nano is not lexically ordered.
	// "…00.5Z" sorts before "…00Z" under plain TEXT comparison because
	// '.' (0x2E) < 'Z' (0x5A), so coverage must use ts_norm.
	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := types.SessionID("sess-frac")
	started := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	session := model.NewSession(sessionID, started, "cli", "codex", "ws")
	boundary, err := model.NewEventWithClock(
		"evt-start", types.EventKindSessionStarted, "cli", "codex", sessionID, "ws", "session started",
		fixedEventClock{at: started},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatal(err)
	}

	// Whole second first in real time, then a fractional half-second later.
	// formatTimestamp trims trailing zeros, so these persist with different widths.
	whole := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	frac := time.Date(2026, 8, 1, 10, 0, 0, 500_000_000, time.UTC) // 0.5s
	for _, seed := range []eventSeed{
		{id: "evt-whole", at: whole},
		{id: "evt-frac", at: frac},
	} {
		event, err := model.NewEventWithClock(
			types.EventID(seed.id), types.EventKindNote, "cli", "codex", sessionID, "ws", "body "+seed.id,
			fixedEventClock{at: seed.at},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.events.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	after, err := fx.events.EventIsStrictlyAfter(ctx, "evt-frac", "evt-whole")
	if err != nil {
		t.Fatalf("EventIsStrictlyAfter() error = %v", err)
	}
	if !after {
		t.Fatal("evt-frac should be strictly after evt-whole under ts_norm order")
	}
	before, err := fx.events.EventIsStrictlyAfter(ctx, "evt-whole", "evt-frac")
	if err != nil {
		t.Fatalf("EventIsStrictlyAfter(reverse) error = %v", err)
	}
	if before {
		t.Fatal("evt-whole must not be after evt-frac")
	}

	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.sut, fx.events, types.SystemClock{})
	first, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "to whole", ProducedBy: "agent", CoversTo: "evt-whole",
	})
	if err != nil {
		t.Fatalf("first Refine() error = %v", err)
	}
	// If lexical order were used, evt-frac would incorrectly look *before*
	// evt-whole and this would no-op. With ts_norm it must supersede.
	second, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "to frac", ProducedBy: "agent", CoversTo: "evt-frac",
	})
	if err != nil {
		t.Fatalf("second Refine() error = %v", err)
	}
	if second.Outcome() != model.SessionRefineOutcomeSuperseded {
		t.Fatalf("Outcome() = %q, want superseded (fractional-second advance)", second.Outcome())
	}
	if second.Refinement().Generation() != first.Refinement().Generation()+1 {
		t.Fatalf("generation = %d, want %d", second.Refinement().Generation(), first.Refinement().Generation()+1)
	}
	if second.Refinement().CoversToEventID() != "evt-frac" {
		t.Fatalf("covers_to = %s, want evt-frac", second.Refinement().CoversToEventID())
	}
}

func TestSessionRefinementDatasource_RetentionDoesNotDeleteRefinements(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)

	sessionID := seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-retain", []eventSeed{
		{id: "evt-old", at: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	})
	uc := usecase.NewSessionRefinementUsecase(fx.sessions, fx.sut, fx.events, types.SystemClock{})
	if _, err := uc.Refine(ctx, usecase.SessionRefineInput{
		SessionID: sessionID, Summary: "must survive gc", ProducedBy: "agent", CoversTo: "evt-old",
	}); err != nil {
		t.Fatalf("Refine() error = %v", err)
	}

	// Aggressive cutoff: everything old is eligible for GC targets that exist.
	cutoff := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := fx.store.CollectGarbage(ctx, cutoff, apptypes.GarbageCollectionTargetAll, false); err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}

	got, err := fx.sut.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := got.Value()
	if !ok {
		t.Fatal("session refinement was deleted by retention/gc")
	}
	if diff := cmp.Diff("must survive gc", row.Summary()); diff != "" {
		t.Fatalf("Summary mismatch (-want +got):\n%s", diff)
	}
	if countSessionRefinementsAt(t, fx.dbPath) != 1 {
		t.Fatalf("session_refinements row count after gc != 1")
	}
}

func TestSessionRefinementDatasource_SaveIfAdvances_CASGuards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newSessionRefinementFixture(t)
	sessionID := seedRefinementSession(ctx, t, fx.sessions, fx.events, "sess-cas", []eventSeed{
		{id: "evt-1", at: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{id: "evt-2", at: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC)},
		{id: "evt-3", at: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
	})

	// Seed generation 1 covering evt-2.
	seed, err := model.NewSessionRefinement(
		sessionID, 1, "sess-cas-start", "evt-2",
		"seed summary", "", "agent",
		time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	written, err := fx.sut.SaveIfAdvances(ctx, seed, 0)
	if err != nil {
		t.Fatalf("seed SaveIfAdvances() error = %v", err)
	}
	if !written {
		t.Fatal("seed SaveIfAdvances() = false, want true")
	}

	// Stale expectedGeneration is rejected; row stays at generation 1.
	stale, err := model.NewSessionRefinement(
		sessionID, 2, "sess-cas-start", "evt-3",
		"stale gen attempt", "", "agent",
		time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Row is at gen 1; claiming expectedGeneration 0 (or any wrong value) fails.
	// Here expected 0 against existing row.
	advanced, err := fx.sut.SaveIfAdvances(ctx, stale, 0)
	if err != nil {
		t.Fatalf("SaveIfAdvances(expectedGen=0) error = %v", err)
	}
	if advanced {
		t.Fatal("SaveIfAdvances(expectedGen=0 against existing) = true, want false")
	}
	assertRefinementUnchanged(ctx, t, fx.sut, sessionID, 1, "evt-2", "seed summary")

	// Stale generation (claim gen 2 when row is gen 1) is rejected.
	staleGen, err := model.NewSessionRefinement(
		sessionID, 3, "sess-cas-start", "evt-3",
		"stale expected gen", "", "agent",
		time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err = fx.sut.SaveIfAdvances(ctx, staleGen, 2)
	if err != nil {
		t.Fatalf("SaveIfAdvances(stale gen) error = %v", err)
	}
	if advanced {
		t.Fatal("SaveIfAdvances(stale expectedGeneration) = true, want false")
	}
	assertRefinementUnchanged(ctx, t, fx.sut, sessionID, 1, "evt-2", "seed summary")

	// Correct expectedGeneration but older covers_to is rejected by order guard.
	older, err := model.NewSessionRefinement(
		sessionID, 2, "sess-cas-start", "evt-1",
		"older covers_to", "", "agent",
		time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err = fx.sut.SaveIfAdvances(ctx, older, 1)
	if err != nil {
		t.Fatalf("SaveIfAdvances(older covers_to) error = %v", err)
	}
	if advanced {
		t.Fatal("SaveIfAdvances(older covers_to with correct gen) = true, want false")
	}
	assertRefinementUnchanged(ctx, t, fx.sut, sessionID, 1, "evt-2", "seed summary")

	// Correct generation + advancing covers_to succeeds.
	next, err := model.NewSessionRefinement(
		sessionID, 2, "sess-cas-start", "evt-3",
		"advanced summary", "", "agent",
		time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err = fx.sut.SaveIfAdvances(ctx, next, 1)
	if err != nil {
		t.Fatalf("SaveIfAdvances(advance) error = %v", err)
	}
	if !advanced {
		t.Fatal("SaveIfAdvances(advance) = false, want true")
	}
	assertRefinementUnchanged(ctx, t, fx.sut, sessionID, 2, "evt-3", "advanced summary")
}

func assertRefinementUnchanged(
	ctx context.Context,
	t *testing.T,
	sut *sqlite.SessionRefinementDatasource,
	sessionID types.SessionID,
	wantGen int,
	wantTo types.EventID,
	wantSummary string,
) {
	t.Helper()
	got, err := sut.FindBySessionID(ctx, sessionID)
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	row, ok := got.Value()
	if !ok {
		t.Fatal("refinement missing")
	}
	if row.Generation() != wantGen || row.CoversToEventID() != wantTo || row.Summary() != wantSummary {
		t.Fatalf("row = gen=%d to=%s summary=%q, want gen=%d to=%s summary=%q",
			row.Generation(), row.CoversToEventID(), row.Summary(),
			wantGen, wantTo, wantSummary)
	}
}

type eventSeed struct {
	id string
	at time.Time
}

type fixedEventClock struct{ at time.Time }

func (c fixedEventClock) Now() time.Time { return c.at }

type sessionRefinementFixture struct {
	dbPath   string
	sut      *sqlite.SessionRefinementDatasource
	sessions *sqlite.SessionDatasource
	events   *sqlite.EventDatasource
	store    *sqlite.StoreManagementDatasource
}

func newSessionRefinementFixture(t *testing.T) sessionRefinementFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	store := sqlite.NewStoreManagementDatasource(database)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return sessionRefinementFixture{
		dbPath:   dbPath,
		sut:      sqlite.NewSessionRefinementDatasource(database),
		sessions: sqlite.NewSessionDatasource(database),
		events:   sqlite.NewEventDatasource(database),
		store:    store,
	}
}

func seedRefinementSession(
	ctx context.Context,
	t *testing.T,
	sessions *sqlite.SessionDatasource,
	events *sqlite.EventDatasource,
	sessionIDValue string,
	seeds []eventSeed,
) types.SessionID {
	t.Helper()
	sessionID := types.SessionID(sessionIDValue)
	started := seeds[0].at
	session := model.NewSession(sessionID, started, "cli", "codex", "ws")
	boundary, err := model.NewEventWithClock(
		types.EventID(sessionIDValue+"-start"),
		types.EventKindSessionStarted,
		"cli",
		"codex",
		sessionID,
		"ws",
		"session started",
		fixedEventClock{at: started.Add(-time.Second)},
	)
	if err != nil {
		t.Fatalf("NewEventWithClock(start) error = %v", err)
	}
	if err := sessions.SaveBoundary(ctx, session, boundary); err != nil {
		t.Fatalf("SaveBoundary() error = %v", err)
	}
	for _, seed := range seeds {
		event, err := model.NewEventWithClock(
			types.EventID(seed.id),
			types.EventKindNote,
			"cli",
			"codex",
			sessionID,
			"ws",
			"body "+seed.id,
			fixedEventClock{at: seed.at},
		)
		if err != nil {
			t.Fatalf("NewEventWithClock(%s) error = %v", seed.id, err)
		}
		if err := events.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", seed.id, err)
		}
	}
	return sessionID
}

func countSessionRefinementsAt(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_refinements`).Scan(&count); err != nil {
		t.Fatalf("COUNT session_refinements error = %v", err)
	}
	return count
}
