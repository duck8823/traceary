package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigrate_CurrentCatalogSkipsDDLWhileImmediateWriterHeld(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations := operatorOpenMigrations(t)
	database := NewDatabase(dbPath, migrations)
	if err := database.initialize(ctx); err != nil {
		t.Fatalf("seed initialize error = %v", err)
	}

	locker, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open locker error = %v", err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	if _, err := locker.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE error = %v", err)
	}
	defer func() { _, _ = locker.ExecContext(ctx, "ROLLBACK") }()

	db, err := database.open(ctx)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	started := time.Now()
	err = database.migrate(ctx, db)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("migrate() on current catalog while IMMEDIATE is held: %v", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("migrate() elapsed = %s, want no busy wait on a current catalog", elapsed)
	}
}

func TestInitialize_CurrentCatalogDoesNotExecuteMigrationSQLUnderExclusiveWriter(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations := operatorOpenMigrations(t)
	if err := NewDatabase(dbPath, migrations).initialize(ctx); err != nil {
		t.Fatalf("seed initialize error = %v", err)
	}

	locker, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open locker error = %v", err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	if _, err := locker.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE error = %v", err)
	}
	defer func() { _, _ = locker.ExecContext(ctx, "ROLLBACK") }()

	err = NewDatabase(dbPath, migrations).initialize(ctx)
	if err != nil && (strings.Contains(err.Error(), "version=73") || strings.Contains(err.Error(), "failed to execute migration SQL")) {
		t.Fatalf("initialize() applied DDL on a current catalog: %v", err)
	}
}

func TestInitialize_OperatorBusyRetryWaitsOutExclusiveWriter(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	migrations := operatorOpenMigrations(t)
	if err := NewDatabase(dbPath, migrations).initialize(ctx); err != nil {
		t.Fatalf("seed initialize error = %v", err)
	}

	locker, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open locker error = %v", err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	if _, err := locker.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE error = %v", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		_, _ = locker.ExecContext(context.Background(), "ROLLBACK")
		close(released)
	}()

	err = retryInitializeOnBusy(ctx, 5*time.Second, func() error {
		return NewDatabase(dbPath, migrations).initialize(ctx)
	})
	<-released
	if err != nil {
		t.Fatalf("operator initialize retry error = %v", err)
	}
}

func retryInitializeOnBusy(ctx context.Context, limit time.Duration, initialize func() error) error {
	deadline := time.Now().Add(limit)
	var err error
	for time.Now().Before(deadline) {
		err = initialize()
		if err == nil || !sqliteBusyError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(50 * time.Millisecond):
		}
	}
	return err
}

func operatorOpenMigrations(t *testing.T) fs.FS {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return os.DirFS(filepath.Join(filepath.Dir(file), "..", "..", "schema", "sqlite", "migrations"))
}
