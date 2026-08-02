package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
)

func TestSQLiteCompactionBuilderBuildAndVerifyPair(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample(id TEXT PRIMARY KEY, body BLOB); INSERT INTO sample VALUES('a',x'0001'),('b','text')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	builder := SQLiteCompactionBuilder{}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Build(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("Build mutated source bytes")
	}
	if err := builder.VerifyPair(ctx, source, candidate); err != nil {
		t.Fatal(err)
	}
	candidateDB, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidateDB.Exec(`UPDATE sample SET body='changed' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if err := candidateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := builder.VerifyPair(ctx, source, candidate); err == nil {
		t.Fatal("VerifyPair accepted logical mismatch")
	}
}

func TestStoreCompactionSmallAllocatedShapeE2E(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sample(id INTEGER PRIMARY KEY, body BLOB); INSERT INTO sample(body) VALUES(zeroblob(1048576)),('queryable')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "journal")}
	service := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := service.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if run.Resources.RequiredBytes == 0 || !run.Resources.ExchangeCapability {
		t.Fatalf("invalid resource plan: %+v", run.Resources)
	}
	run, err = service.Apply(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", run.Phase)
	}
	compacted, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if compacted.Inode == original.Inode {
		t.Fatal("source inode did not exchange")
	}
	read, err := sql.Open("sqlite", sqliteReadOnlyDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := read.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if count != 2 {
		t.Fatalf("count=%d", count)
	}
	run, err = service.Rollback(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != domain.CompactionRolledBack {
		t.Fatalf("rollback phase=%s", run.Phase)
	}
	restored, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inode != original.Inode {
		t.Fatalf("original inode not restored: %d != %d", restored.Inode, original.Inode)
	}
}

func directSQLiteRWDSNCreate(path string) string { return "file:" + path }
