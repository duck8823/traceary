package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestCompactFailsWhenEveryDiscardableSessionIsUnrefined(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	old := newGCEventFixture(t, "event-old", types.EventKindTranscript, "why-is-here", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertGCSession(t, db, "session-1", true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	_, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Compact() error = nil, want unrefined refusal")
	}
	var unrefined application.UnrefinedMaterialError
	if !asUnrefined(err, &unrefined) {
		t.Fatalf("Compact() error = %v, want UnrefinedMaterialError", err)
	}
	if unrefined.Sessions < 1 {
		t.Fatalf("unrefined sessions = %d", unrefined.Sessions)
	}
}

func TestCompactReclaimsFoldedSessionAndLeavesUnrefined(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
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

	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.UnrefinedRemaining < 1 {
		t.Fatalf("UnrefinedRemaining = %d, want the unfolder session left", got.UnrefinedRemaining)
	}

	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-folded"); avail != "unavailable_retention" {
		t.Fatalf("folded body availability = %s, want unavailable_retention", avail)
	}
	if avail := gcEventAvailability(t, db, "event-open"); avail != "available" {
		t.Fatalf("unrefined body availability = %s, want available", avail)
	}
	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() <= 0 || before.Size() <= 0 {
		t.Fatalf("unexpected sizes before=%d after=%d", before.Size(), after.Size())
	}
}

func asUnrefined(err error, dest *application.UnrefinedMaterialError) bool {
	return errors.As(err, dest)
}

func TestVerifyPairRejectsEmptyEventsCandidateWhenSourceHasUniqueEvents(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	unique := newGCEventFixture(t, "event-unique", types.EventKindTranscript, "unique-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), unique); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of emptied unique events")
	}
}

func TestVerifyPairRejectsRewrittenAvailableBody(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	body := newGCEventFixture(t, "event-body", types.EventKindTranscript, "original-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`UPDATE events SET body = 'tampered-why' WHERE id = 'event-body'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of rewritten body")
	}
}

func TestVerifyPairRejectsCrossIdentitySameBodyDeletion(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	first := newGCEventFixture(t, "event-a", types.EventKindTranscript, "shared-body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	second := newGCEventFixture(t, "event-b", types.EventKindTranscript, "shared-body", time.Date(2026, 6, 1, 0, 0, 1, 0, time.UTC))
	if err := events.Save(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := events.Save(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-other' WHERE id = 'event-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db = openRetentionDB(t, candidate)
	if _, err := db.Exec(`DELETE FROM events WHERE id = 'event-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of a same-body drop from another session")
	}
}

func TestVerifyPairRejectsSourceHookAndCreatedAtNormDrift(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	body := newGCEventFixture(t, "event-ident", types.EventKindTranscript, "identity-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{name: "source_hook", sql: `UPDATE events SET source_hook = 'tampered-hook' WHERE id = 'event-ident'`},
		{name: "created_at_norm", sql: `UPDATE events SET created_at_norm = '2099-01-01T00:00:00.000000000Z' WHERE id = 'event-ident'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := filepath.Join(filepath.Dir(dbPath), "candidate-"+tc.name+".db")
			if err := os.WriteFile(candidate, data, 0o600); err != nil {
				t.Fatal(err)
			}
			db := openRetentionDB(t, candidate)
			if _, err := db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
				t.Fatalf("VerifyPair() error = nil, want rejection of %s drift", tc.name)
			}
		})
	}
}

func TestVerifyPairRejectsUndecodableAvailableCandidate(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	body := newGCEventFixture(t, "event-codec", types.EventKindTranscript, "readable-why", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`
		UPDATE events
		   SET body_codec = 'zstd',
		       body_format_version = 1,
		       body_plaintext_bytes = 12,
		       body_encoded_bytes = length(CAST(body AS BLOB)),
		       body_sha256 = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
		 WHERE id = 'event-codec'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of an undecodable available body")
	}
}

func TestCompactForceCoverCompletesOnRealStore(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	old := newGCEventFixture(t, "event-old", types.EventKindTranscript, "why-is-here", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-1", true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	migrations := onDiskSQLiteMigrations(t)
	usecase.BindCompactionWorkCover(svc, func(ctx context.Context, work string, cutoff time.Time) error {
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
			StaleAfter: 24 * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		if err := application.ForceCoverSafeToDelete(
			result.HasMore(), result.EarliestUnprocessedEventTime(),
			result.Skipped(), result.EarliestSkippedEventTime(),
			cutoff,
		); err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		return nil
	})

	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		Force:    true,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.UnrefinedRemaining != 0 {
		t.Fatalf("UnrefinedRemaining = %d, want 0 after completed --force cover", got.UnrefinedRemaining)
	}
	if !got.MechanicalSummaries {
		t.Fatal("MechanicalSummaries = false, want true")
	}
	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-old"); avail != "unavailable_retention" {
		t.Fatalf("forced body availability = %s, want unavailable_retention", avail)
	}
}

// TestCompactForceCoverProceedsWhenLeftoverIsNewerThanCutoff pins the #1721
// fix: an already-covered, discardable-age session must still be deleted even
// though a bounded cover pass leaves part of the orphan backlog unfolded, as
// long as that leftover is entirely newer than the retention cutoff.
// Discovery orders oldest-first, so a Limit of 1 folds session-new-a and
// leaves session-new-b (newer still) as the reported leftover; both are newer
// than cutoff, so the safe lower bound the cover reports (session-new-a's
// earliest event time) is newer than cutoff too.
func TestCompactForceCoverProceedsWhenLeftoverIsNewerThanCutoff(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	old := newGCEventFixture(t, "event-old", types.EventKindTranscript, "why-is-here", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	newA := newGCEventFixture(t, "event-new-a", types.EventKindTranscript, "why-is-here", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), newA); err != nil {
		t.Fatal(err)
	}
	newB := newGCEventFixture(t, "event-new-b", types.EventKindTranscript, "why-is-here", time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), newB); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, dbPath)
	insertGCSession(t, db, "session-covered", true)
	insertGCSession(t, db, "session-new-a", true)
	insertGCSession(t, db, "session-new-b", true)
	insertGCFold(t, db, "session-covered", "event-old", "event-old")
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-covered' WHERE id = 'event-old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-new-a' WHERE id = 'event-new-a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE events SET session_id = 'session-new-b' WHERE id = 'event-new-b'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	migrations := onDiskSQLiteMigrations(t)
	usecase.BindCompactionWorkCover(svc, func(ctx context.Context, work string, cutoff time.Time) error {
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
			StaleAfter: 24 * time.Hour,
			Limit:      1,
		})
		if err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		if err := application.ForceCoverSafeToDelete(
			result.HasMore(), result.EarliestUnprocessedEventTime(),
			result.Skipped(), result.EarliestSkippedEventTime(),
			cutoff,
		); err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		return nil
	})

	if _, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		Force:    true,
		KeepDays: 10,
		Now:      time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Compact() error = %v, want compact to proceed: the unfolded leftover is newer than the cutoff", err)
	}

	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "event-old"); avail != "unavailable_retention" {
		t.Fatalf("forced body availability = %s, want unavailable_retention for the already-covered pre-cutoff session", avail)
	}
}

// TestCompactForceCoverRefusesWhenLeftoverIsOlderThanCutoff is the regression
// pinned by #1721: a bounded cover pass that leaves behind a range whose
// earliest event is older than the retention cutoff must refuse rather than
// let CollectGarbage discard material no fold has ever seen.
func TestCompactForceCoverRefusesWhenLeftoverIsOlderThanCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := newOrphanFixture(t)

	seedOrphanSession(ctx, t, fx, "session-old-1", []eventSeed{
		{id: "evt-old-1", at: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
	}, true)
	seedOrphanSession(ctx, t, fx, "session-old-2", []eventSeed{
		{id: "evt-old-2", at: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)},
	}, true)

	svc := usecase.NewStoreCompactionUsecase(
		fx.dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	migrations := onDiskSQLiteMigrations(t)
	usecase.BindCompactionWorkCover(svc, func(ctx context.Context, work string, cutoff time.Time) error {
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
			StaleAfter: 24 * time.Hour,
			Limit:      1,
		})
		if err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		if err := application.ForceCoverSafeToDelete(
			result.HasMore(), result.EarliestUnprocessedEventTime(),
			result.Skipped(), result.EarliestSkippedEventTime(),
			cutoff,
		); err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		return nil
	})

	_, err := svc.Compact(ctx, application.CompactInput{
		Source:   fx.dbPath,
		Force:    true,
		KeepDays: 10,
		Now:      time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Compact() error = nil, want refusal: the unfolded leftover (session-old-2) is older than the cutoff")
	}
	if !strings.Contains(err.Error(), "may be older than the retention cutoff") {
		t.Fatalf("Compact() error = %v, want it to name the retention cutoff as the reason", err)
	}

	db := openRetentionDB(t, fx.dbPath)
	defer func() { _ = db.Close() }()
	if avail := gcEventAvailability(t, db, "evt-old-1"); avail == "unavailable_retention" {
		t.Fatal("evt-old-1 body_availability = unavailable_retention, want unchanged: a refused compact must not have touched the original store")
	}
}

const emptyIdentitySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestCompactClearsDuplicatedCommandExecutedBodiesAndKeepsLogOnlyBodies(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	composed := strings.Repeat("command + INPUT + OUTPUT\n", 80)
	saveHistoricalCommandExecuted(t, events, "cmd-dup", composed, true)
	saveHistoricalCommandExecuted(t, events, "cmd-log", "log-only body", false)

	svc := newTestCompactionUsecase(t, dbPath)
	got, err := svc.Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.ReleasedCommandBodyRows != 1 {
		t.Fatalf("ReleasedCommandBodyRows = %d, want 1", got.ReleasedCommandBodyRows)
	}
	if got.ReleasedCommandBodyBytes <= 0 {
		t.Fatalf("ReleasedCommandBodyBytes = %d, want measured stored bytes", got.ReleasedCommandBodyBytes)
	}

	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	assertClearedCommandBody(t, db, "cmd-dup")
	var logBody string
	if err := db.QueryRow(`SELECT CAST(body AS TEXT) FROM events WHERE id = 'cmd-log'`).Scan(&logBody); err != nil {
		t.Fatal(err)
	}
	if logBody != "log-only body" {
		t.Fatalf("log-only body = %q, want preserved", logBody)
	}
	var auditCommand string
	if err := db.QueryRow(`SELECT command_text FROM command_audits WHERE event_id = 'cmd-dup'`).Scan(&auditCommand); err != nil {
		t.Fatal(err)
	}
	if auditCommand != "echo duplicated" {
		t.Fatalf("audit command = %q, want echo duplicated", auditCommand)
	}
}

func TestCompactReportsStoredBlobBytesNotPlaintext(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	plain := strings.Repeat("aaaaaaaaaa", 400)
	saveHistoricalCommandExecuted(t, events, "cmd-zstd", plain, true)

	db := openRetentionDB(t, dbPath)
	var stored, plaintext int64
	if err := db.QueryRow(`
SELECT length(CAST(body AS BLOB)), COALESCE(body_plaintext_bytes, length(CAST(body AS BLOB)))
  FROM events WHERE id = 'cmd-zstd'`).Scan(&stored, &plaintext); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if stored <= 0 || plaintext <= stored {
		t.Fatalf("fixture stored=%d plaintext=%d, want compressed stored < plaintext", stored, plaintext)
	}

	got, err := newTestCompactionUsecase(t, dbPath).Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 90,
		Now:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got.ReleasedCommandBodyBytes != stored {
		t.Fatalf("ReleasedCommandBodyBytes = %d, want stored blob %d (not plaintext %d)", got.ReleasedCommandBodyBytes, stored, plaintext)
	}
}

func TestVerifyPairAllowsClearedCommandExecutedBodyWithAudit(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	saveHistoricalCommandExecuted(t, events, "cmd-dup", "duplicated body", true)
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	emptyCommandExecutedBody(t, db, "cmd-dup")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate); err != nil {
		t.Fatalf("VerifyPair() error = %v, want permitted empty command_executed body", err)
	}
}

func TestVerifyPairRejectsClearedCommandExecutedBodyWithoutAudit(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	_ = store
	saveHistoricalCommandExecuted(t, events, "cmd-log", "log-only body", false)
	candidate := filepath.Join(filepath.Dir(dbPath), "candidate.db")
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	emptyCommandExecutedBody(t, db, "cmd-log")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err = (sqlite.SQLiteCompactionBuilder{}).VerifyPair(context.Background(), dbPath, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of a log-only body clear")
	}
	if !strings.Contains(err.Error(), "rewrote body") {
		t.Fatalf("VerifyPair() error = %v, want rewritten-body rejection", err)
	}
}

func newTestCompactionUsecase(t *testing.T, dbPath string) application.StoreCompactionUsecase {
	t.Helper()
	return usecase.NewStoreCompactionUsecase(
		dbPath,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		&sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
}

func saveHistoricalCommandExecuted(t *testing.T, events *sqlite.EventDatasource, id, body string, withAudit bool) {
	t.Helper()
	event := newGCEventFixture(t, id, types.EventKindCommandExecuted, body, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if !withAudit {
		if err := events.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
		return
	}
	audit, err := model.NewCommandAudit(event.EventID(), "echo duplicated", "in", "out", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
	}
	if err := events.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit(%s) error = %v", id, err)
	}
}

func dropRetiredSearchFamily(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range []string{"event_search_documents", "event_search_fts", "event_search_backfill_state"} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			t.Fatalf("drop %s: %v", name, err)
		}
	}
}

func emptyCommandExecutedBody(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`
		UPDATE events
		   SET body = '',
		       body_codec = 'identity',
		       body_format_version = 1,
		       body_plaintext_bytes = 0,
		       body_encoded_bytes = 0,
		       body_sha256 = ?
		 WHERE id = ?`, emptyIdentitySHA256, id); err != nil {
		t.Fatal(err)
	}
}

func assertClearedCommandBody(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	var body, codec, sha string
	var plaintext, encoded int64
	if err := db.QueryRow(`
SELECT CAST(body AS TEXT), body_codec, body_plaintext_bytes, body_encoded_bytes, body_sha256
  FROM events WHERE id = ?`, id).Scan(&body, &codec, &plaintext, &encoded, &sha); err != nil {
		t.Fatal(err)
	}
	if body != "" || codec != "identity" || plaintext != 0 || encoded != 0 || sha != emptyIdentitySHA256 {
		t.Fatalf("cleared body = body=%q codec=%s plain=%d enc=%d sha=%s", body, codec, plaintext, encoded, sha)
	}
}
