package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	planningJournal := &CompactionFileJournal{Dir: filepath.Join(dir, "planning-journal")}
	planner := usecase.NewStoreCompactionUsecase(source, planningJournal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if run.Resources.RequiredBytes == 0 || !run.Resources.ExchangeCapability {
		t.Fatalf("invalid resource plan: %+v", run.Resources)
	}
	if run.Resources.LeaseCapability {
		t.Fatal("plan overstated unavailable cross-process lease")
	}
	if _, err := planner.Apply(ctx, run.ID); err == nil {
		t.Fatal("production apply did not fail closed without cross-process lease")
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "apply-journal")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
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

func TestStoreCompactionResumeReplacesRunOwnedNearCapacityPartialCandidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE payloads(id INTEGER PRIMARY KEY, body BLOB); INSERT INTO payloads(body) VALUES(zeroblob(16777216)); CREATE TABLE probe(v TEXT); INSERT INTO probe VALUES('intact')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	plannerJournal := &CompactionFileJournal{Dir: filepath.Join(dir, "planner")}
	planner := usecase.NewStoreCompactionUsecase(source, plannerJournal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "resume")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, err = run.Advance(domain.CompactionCopyIntent, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	partialSize := len(data) * 9 / 10
	if err := os.WriteFile(run.CandidatePath, data[:partialSize], 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("source=%d partial=%d", len(data), partialSize)
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	got, err := fresh.Resume(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s", got.Phase)
	}
	read, err := sql.Open("sqlite", sqliteReadOnlyDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := read.QueryRow(`SELECT v FROM probe`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if value != "intact" {
		t.Fatalf("value=%q", value)
	}
}

func TestStoreCompactionResumePreservesUnknownValidCandidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	_, _ = db.Exec(`CREATE TABLE expected(v TEXT); INSERT INTO expected VALUES('source')`)
	_ = db.Close()
	plannerJournal := &CompactionFileJournal{Dir: filepath.Join(dir, "planner")}
	planner := usecase.NewStoreCompactionUsecase(source, plannerJournal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "resume")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, _ = run.Advance(domain.CompactionCopyIntent, time.Now())
	if err := journal.Append(ctx, run); err != nil {
		t.Fatal(err)
	}
	other, _ := sql.Open("sqlite", directSQLiteRWDSNCreate(run.CandidatePath))
	_, _ = other.Exec(`CREATE TABLE unrelated(v TEXT); INSERT INTO unrelated VALUES('preserve')`)
	_ = other.Close()
	before, err := os.ReadFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	fresh := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	if _, err := fresh.Resume(ctx, run.ID); err == nil {
		t.Fatal("Resume accepted unknown valid candidate")
	}
	after, err := os.ReadFile(run.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("unknown candidate was mutated")
	}
	loaded, err := journal.Load(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != domain.CompactionCopyIntent {
		t.Fatalf("journal advanced to %s", loaded.Phase)
	}
}

func directSQLiteRWDSNCreate(path string) string { return "file:" + path }
