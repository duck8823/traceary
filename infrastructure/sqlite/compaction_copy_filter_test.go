package sqlite_test

import (
	"context"
	"errors"
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
