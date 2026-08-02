package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRehearsalMigrationPlanMeasuresPendingWALAndExplicitSkip(t *testing.T) {
	ctx := context.Background()
	all, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(t.TempDir(), "pending.db")
	if err = NewDatabase(pendingPath, migrationsBeforeCodecFoundation(t)).initialize(ctx); err != nil {
		t.Fatal(err)
	}
	pendingPlan, err := NewDatabase(pendingPath, all).measureRehearsalMigrationWAL(ctx, pendingPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !pendingPlan.pending || pendingPlan.journalMode != "wal" || pendingPlan.walBytes <= 0 || pendingPlan.elapsed <= 0 || pendingPlan.elapsed > time.Second {
		t.Fatalf("pending migration plan=%+v", pendingPlan)
	}
	currentPath := filepath.Join(t.TempDir(), "current.db")
	current := NewDatabase(currentPath, all)
	if err = current.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	currentPlan, err := current.measureRehearsalMigrationWAL(ctx, currentPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if currentPlan.pending || currentPlan.walBytes != 0 || currentPlan.elapsed != 0 || currentPlan.journalMode != "wal" {
		t.Fatalf("no-pending migration plan=%+v", currentPlan)
	}
}

func TestNormalizeRehearsalJournalHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	all, _ := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err := NewDatabase(path, all).initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", writableRehearsalDSN(path, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = normalizeRehearsalJournal(ctx, db, time.Second); err == nil {
		t.Fatal("canceled journal normalization succeeded")
	}
}
