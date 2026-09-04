package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestCompactCoversUnrefinedSessionsByDefault(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	oldA := newGCEventFixture(t, "event-a", types.EventKindTranscript, "why-a", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	oldB := newGCEventFixture(t, "event-b", types.EventKindTranscript, "why-b", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), oldA); err != nil {
		t.Fatal(err)
	}
	if err := events.Save(context.Background(), oldB); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-a", true)
	insertGCSession(t, db, "session-b", true)
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-a' WHERE id = 'event-a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-b' WHERE id = 'event-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := newDefaultCoverCompaction(t, dbPath)
	bindRealCover(t, svc)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	want := struct {
		CoveredSessions     int
		MechanicalSummaries bool
		UnrefinedRemaining  int
	}{CoveredSessions: 2, MechanicalSummaries: true, UnrefinedRemaining: 0}
	gotFields := struct {
		CoveredSessions     int
		MechanicalSummaries bool
		UnrefinedRemaining  int
	}{got.CoveredSessions, got.MechanicalSummaries, got.UnrefinedRemaining}
	if diff := cmp.Diff(want, gotFields); diff != "" {
		t.Fatalf("cover fields mismatch (-want +got):\n%s", diff)
	}
	if got.DiscardedBodyBytes <= 0 {
		t.Fatalf("DiscardedBodyBytes = %d, want > 0", got.DiscardedBodyBytes)
	}
	if _, ok := got.Steps.Find("dedupe_archive"); ok {
		t.Fatal(`store compact still reports a "dedupe_archive" step`)
	}
	step, ok := got.Steps.Find(application.CompactStepMechanicalCover)
	if !ok {
		t.Fatal("mechanical_cover step missing")
	}
	if step.Rows != 2 {
		t.Fatalf("mechanical_cover rows = %d, want 2", step.Rows)
	}
	if step.Detail["sessions_after"] != 0 {
		t.Fatalf("sessions_after = %d, want 0", step.Detail["sessions_after"])
	}
	if step.BytesReclaimed != 0 {
		t.Fatalf("BytesReclaimed = %d, want 0", step.BytesReclaimed)
	}

	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-a"); avail != "unavailable_retention" {
		t.Fatalf("event-a availability = %s, want unavailable_retention", avail)
	}
	if avail := gcEventAvailability(t, db, "event-b"); avail != "unavailable_retention" {
		t.Fatalf("event-b availability = %s, want unavailable_retention", avail)
	}
}

func TestCompactLeavesRefinedSessionsUntouched(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	folded := newGCEventFixture(t, "event-folded", types.EventKindTranscript, "folded-body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	open := newGCEventFixture(t, "event-open", types.EventKindTranscript, "open-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), folded); err != nil {
		t.Fatal(err)
	}
	if err := events.Save(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-folded", true)
	insertGCSession(t, db, "session-open", true)
	insertGCFold(t, db, "session-folded", "event-folded", "event-folded")
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-folded' WHERE id = 'event-folded'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-open' WHERE id = 'event-open'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := newDefaultCoverCompaction(t, dbPath)
	bindRealCover(t, svc)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.CoveredSessions != 1 {
		t.Fatalf("CoveredSessions = %d, want 1", got.CoveredSessions)
	}

	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if n := countSessionRefinements(t, db, "session-folded"); n != 1 {
		t.Fatalf("session-folded refinements = %d, want 1 (no mechanical row)", n)
	}
	if n := countDegradedRefinements(t, db, "session-open"); n != 1 {
		t.Fatalf("session-open degraded refinements = %d, want 1", n)
	}
	if avail := gcEventAvailability(t, db, "event-folded"); avail != "unavailable_retention" {
		t.Fatalf("folded body availability = %s, want unavailable_retention", avail)
	}
	if avail := gcEventAvailability(t, db, "event-open"); avail != "unavailable_retention" {
		t.Fatalf("unrefined body availability = %s, want unavailable_retention", avail)
	}
}

func TestCompactRefuseUnrefinedKeepsPolicyStop(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	old := newGCEventFixture(t, "event-old", types.EventKindTranscript, "why-is-here", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-1", true)
	insertGCSession(t, db, "session-2", true)
	oldB := newGCEventFixture(t, "event-old-b", types.EventKindTranscript, "why-b", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := events.Save(context.Background(), oldB); err != nil {
		t.Fatal(err)
	}
	db = openRetentionDB(t, dbPath)
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-2' WHERE id = 'event-old-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	journalDir := filepath.Join(t.TempDir(), "journal")
	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: journalDir},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	_, err := svc.Compact(context.Background(), application.CompactInput{
		Source:          dbPath,
		RefuseUnrefined: true,
		KeepDays:        90,
		Now:             time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Compact() error = nil, want UnrefinedMaterialError")
	}
	var unrefined application.UnrefinedMaterialError
	if !errors.As(err, &unrefined) {
		t.Fatalf("Compact() error = %v, want UnrefinedMaterialError", err)
	}
	if unrefined.Sessions != 2 {
		t.Fatalf("unrefined sessions = %d, want 2", unrefined.Sessions)
	}
	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-old"); avail != "available" {
		t.Fatalf("event-old availability = %s, want available", avail)
	}
	if avail := gcEventAvailability(t, db, "event-old-b"); avail != "available" {
		t.Fatalf("event-old-b availability = %s, want available", avail)
	}
	if entries, readErr := os.ReadDir(journalDir); readErr == nil && len(entries) != 0 {
		t.Fatalf("journal entries = %v, want none", entries)
	}
}

func TestCompactRefuseUnrefinedProceedsOnPartialFold(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	folded := newGCEventFixture(t, "event-folded", types.EventKindTranscript, "folded-body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	open := newGCEventFixture(t, "event-open", types.EventKindTranscript, "open-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), folded); err != nil {
		t.Fatal(err)
	}
	if err := events.Save(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-folded", true)
	insertGCSession(t, db, "session-open", true)
	insertGCFold(t, db, "session-folded", "event-folded", "event-folded")
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-folded' WHERE id = 'event-folded'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-open' WHERE id = 'event-open'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := newDefaultCoverCompaction(t, dbPath)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:          dbPath,
		RefuseUnrefined: true,
		KeepDays:        90,
		Now:             time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.CoveredSessions != 0 || got.MechanicalSummaries || got.UnrefinedRemaining != 1 {
		t.Fatalf("CoveredSessions=%d MechanicalSummaries=%v UnrefinedRemaining=%d", got.CoveredSessions, got.MechanicalSummaries, got.UnrefinedRemaining)
	}
	if _, ok := got.Steps.Find(application.CompactStepMechanicalCover); ok {
		t.Fatal("mechanical_cover step present, want absent")
	}
	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-folded"); avail != "unavailable_retention" {
		t.Fatalf("folded body availability = %s, want unavailable_retention", avail)
	}
	if avail := gcEventAvailability(t, db, "event-open"); avail != "available" {
		t.Fatalf("unrefined body availability = %s, want available", avail)
	}
}

func TestCompactWithoutBoundCoverFailsWhenUnrefinedExists(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	old := newGCEventFixture(t, "event-old", types.EventKindTranscript, "why-is-here", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-1", true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := newDefaultCoverCompaction(t, dbPath)
	_, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Compact() error = nil, want unbound cover")
	}
	if !strings.Contains(err.Error(), "none is bound") || !strings.Contains(err.Error(), "unrefined session") {
		t.Fatalf("Compact() error = %v, want unbound mechanical cover naming the unrefined count", err)
	}
	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-old"); avail != "available" {
		t.Fatalf("source availability = %s, want available", avail)
	}
}

func TestCompactInPlaceCoversByDefault(t *testing.T) {
	t.Parallel()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	old := newGCEventFixture(t, "event-old", types.EventKindTranscript, "why-is-here", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-1", true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	builder := &inPlaceOnlyBuilder{inner: &sqlite.SQLiteCompactionBuilder{}}
	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		builder,
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	bindRealCover(t, svc)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.CompactStrategy != application.CompactStrategyInPlace {
		t.Fatalf("CompactStrategy = %s, want in_place", got.CompactStrategy)
	}
	step, ok := got.Steps.Find(application.CompactStepMechanicalCover)
	if !ok {
		t.Fatal("mechanical_cover step missing on in-place path")
	}
	if step.Rows <= 0 {
		t.Fatalf("mechanical_cover rows = %d, want > 0", step.Rows)
	}
	if step.Detail["refinements_produced"] <= 0 {
		t.Fatalf("refinements_produced = %d, want > 0", step.Detail["refinements_produced"])
	}
}

func newDefaultCoverCompaction(t *testing.T, dbPath string) application.StoreCompactionUsecase {
	t.Helper()
	return usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
}

func bindRealCover(t *testing.T, svc application.StoreCompactionUsecase) {
	t.Helper()
	migrations := onDiskSQLiteMigrations(t)
	usecase.BindCompactionWorkCover(svc, func(ctx context.Context, work string, cutoff time.Time) (application.CoverReport, error) {
		database := sqlite.NewDatabase(work, migrations)
		refine := usecase.NewSessionRefinementUsecase(
			sqlite.NewSessionDatasource(database),
			sqlite.NewSessionRefinementDatasource(database),
			sqlite.NewEventDatasource(database),
			types.SystemClock{},
		)
		cover := usecase.NewOrphanConsolidationUsecase(
			sqlite.NewSessionOrphanRangeDatasource(database),
			refine,
			types.SystemClock{},
		)
		result, err := cover.Consolidate(ctx, usecase.OrphanConsolidationInput{
			StaleAfter:      24 * time.Hour,
			RetentionCutoff: cutoff,
		})
		if err != nil {
			return application.CoverReport{}, fmt.Errorf("compact mechanical cover: %w", err)
		}
		if err := application.ForceCoverSafeToDelete(
			result.HasMore(), result.EarliestUnprocessedEventTime(),
			result.Skipped(), result.EarliestSkippedEventTime(),
			cutoff,
		); err != nil {
			return application.CoverReport{}, fmt.Errorf("compact mechanical cover: %w", err)
		}
		return coverReportFrom(result), nil
	})
}

func countSessionRefinements(t *testing.T, db *sql.DB, sessionID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_refinements WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countDegradedRefinements(t *testing.T, db *sql.DB, sessionID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_refinements WHERE session_id = ? AND degraded = 1`, sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type inPlaceOnlyBuilder struct {
	inner *sqlite.SQLiteCompactionBuilder
}

func (b *inPlaceOnlyBuilder) SetCompactFilter(filter application.CompactFilter) {
	b.inner.SetCompactFilter(filter)
}

func (b *inPlaceOnlyBuilder) InspectBodyGate(ctx context.Context, source string, cutoff time.Time) (application.BodyGate, error) {
	gate, err := b.inner.InspectBodyGate(ctx, source, cutoff)
	if err != nil {
		return application.BodyGate{}, fmt.Errorf("inspect body gate: %w", err)
	}
	return gate, nil
}

func (b *inPlaceOnlyBuilder) InspectCommandBodyReclaim(ctx context.Context, source string) (application.CommandBodyReclaim, error) {
	reclaim, err := sqlite.SQLiteCompactionBuilder{}.InspectCommandBodyReclaim(ctx, source)
	if err != nil {
		return application.CommandBodyReclaim{}, fmt.Errorf("inspect command body reclaim: %w", err)
	}
	return reclaim, nil
}

func (b *inPlaceOnlyBuilder) Build(context.Context, string, string) error {
	return fmt.Errorf("insufficient free space for compaction replica")
}

func (b *inPlaceOnlyBuilder) ClassifyCandidate(context.Context, string, string) (domain.CandidateCondition, error) {
	return domain.CandidateConditionComplete, nil
}

func (*inPlaceOnlyBuilder) Sync(context.Context, string) error { return nil }

func (*inPlaceOnlyBuilder) VerifyPair(context.Context, string, string) error { return nil }

func (b *inPlaceOnlyBuilder) CompactInPlace(ctx context.Context, source string, filter application.CompactFilter) error {
	if err := b.inner.CompactInPlace(ctx, source, filter); err != nil {
		return fmt.Errorf("in-place compact: %w", err)
	}
	return nil
}
