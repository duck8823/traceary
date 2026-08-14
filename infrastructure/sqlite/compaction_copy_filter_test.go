package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
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
	usecase.BindCompactionWorkCover(svc, func(ctx context.Context, work string) error {
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
			Unlimited:  true,
		})
		if err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		if err := application.ForceCoverMustComplete(result.HasMore(), result.Skipped()); err != nil {
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
