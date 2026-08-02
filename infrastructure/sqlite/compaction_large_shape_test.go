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

func TestCompactionE2E_21Point4GiBShape(t *testing.T) {
	if os.Getenv("TRACEARY_RUN_21GB_COMPACTION_SHAPE") != "1" {
		t.Skip("set TRACEARY_RUN_21GB_COMPACTION_SHAPE=1 for the large-shape harness")
	}
	const shapeBytes = uint64(21_400_000_000)
	dir := t.TempDir()
	available, err := availableBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Source allocation + compact candidate + 10% margin. Report the exact
	// requirement instead of beginning a run that cannot preserve the source.
	required := shapeBytes*2 + shapeBytes/10
	if available < required {
		t.Skipf("21.4 GiB E2E requires %d bytes, available %d bytes", required, available)
	}
	source := filepath.Join(dir, "store.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA page_size=65536; VACUUM; CREATE TABLE shape(id INTEGER PRIMARY KEY, payload BLOB NOT NULL); WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<335) INSERT INTO shape(payload) SELECT zeroblob(64000000) FROM n; CREATE TABLE probe(value TEXT); INSERT INTO probe VALUES('queryable-after-exchange')`); err != nil {
		_ = db.Close()
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
	run, err := planner.Plan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resource plan: required=%d destination=%d temporary=%d margin=%d available=%d", run.Resources.RequiredBytes, run.Resources.DestinationBytes, run.Resources.TemporaryBytes, run.Resources.SafetyMarginBytes, run.Resources.AvailableBytes)
	if run.Resources.LeaseCapability {
		t.Fatal("plan overstated unavailable cross-process lease")
	}
	run.Resources.LeaseCapability = true
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "apply-journal")}
	if err := journal.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	service := usecase.NewStoreCompactionUsecase(source, journal, SQLiteCompactionBuilder{}, StoreReplacementFiles{}, StoreLeaseCoordinator{})
	run, err = service.Apply(context.Background(), run.ID)
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
		t.Fatal("atomic exchange did not change source inode")
	}
	read, err := sql.Open("sqlite", sqliteReadOnlyDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := read.QueryRow(`SELECT value FROM probe`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if value != "queryable-after-exchange" || integrity != "ok" {
		t.Fatalf("post-exchange query=%q integrity=%q", value, integrity)
	}
	run, err = service.Rollback(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := inspectRegularFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inode != original.Inode || run.Phase != domain.CompactionRolledBack {
		t.Fatalf("rollback inode=%d phase=%s", restored.Inode, run.Phase)
	}
}
