package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestPreparedMigrationPublishesAndRollsBackOwnedCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("prepared migration publication exercises the filesystem protocol")
	}
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	v34 := fstest.MapFS{}
	paths, err := fs.Glob(all, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		var version int
		if _, err = fmt.Sscanf(filepath.Base(path), "%06d_", &version); err != nil || version > 34 {
			continue
		}
		body, readErr := fs.ReadFile(all, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		v34[path] = &fstest.MapFile{Data: body, Mode: 0o600}
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "copy.db")
	opened, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = opened.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		var version int
		if _, err = fmt.Sscanf(filepath.Base(path), "%06d_", &version); err != nil || version > 34 {
			continue
		}
		body := v34[path].Data
		tx, txErr := opened.Begin()
		if txErr != nil {
			t.Fatal(txErr)
		}
		if _, txErr = tx.Exec(string(body)); txErr == nil {
			_, txErr = tx.Exec(`INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, version, filepath.Base(path), time.Now().UTC().Format(time.RFC3339Nano))
		}
		if txErr != nil {
			_ = tx.Rollback()
			t.Fatal(txErr)
		}
		if txErr = tx.Commit(); txErr != nil {
			t.Fatal(txErr)
		}
	}
	if err = opened.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedMigrationCandidateRecipe{Migrations: all, Verifier: sqlite.PreparedMigrationVerifier{Migrations: all}}
	service := usecase.NewPreparedStoreUpgradeUsecase(target, journal, sqlite.PreparedStoreUpgradeFiles{}, sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration: recipe})
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Plan(context.Background(), application.PreparedStoreUpgradeCommand{Operation: domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration, TargetPath: target, ConsumerBinding: "test-binding", Budget: domain.PreparedStoreUpgradeBudget{WallTimeLimit: time.Minute, PublishLockLimit: time.Second, OwnedDiskByteLimit: uint64(info.Size())*4 + 1<<30, WALByteLimit: 1 << 30, TemporaryByteLimit: 1 << 30, SafetyMarginBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Prepare(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	receipt, err := service.Publish(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("publication lock exceeded one second: %s", elapsed)
	}
	if receipt.Evidence.MigrationSetDigest != run.PlanDigest {
		t.Fatal("receipt lost exact migration binding")
	}
	currentDB, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	var maxVersion int
	if err = currentDB.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&maxVersion); err != nil || maxVersion != 72 {
		t.Fatalf("published version=%d err=%v", maxVersion, err)
	}
	// The rehearsal legitimately mutates the published candidate inode. Atomic
	// rollback must fence object identity, mode, and links without requiring the
	// original candidate size/mtime to remain frozen.
	if _, err = currentDB.Exec(`CREATE TABLE rehearsal_shadow_write(id INTEGER PRIMARY KEY, body BLOB)`); err != nil {
		t.Fatal(err)
	}
	if err = currentDB.Close(); err != nil {
		t.Fatal(err)
	}
	rolled, err := service.Rollback(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Phase != domain.PreparedStoreUpgradeRolledBack {
		t.Fatalf("rollback phase = %s", rolled.Phase)
	}
	restored, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatal("rollback did not restore original database bytes")
	}
}
