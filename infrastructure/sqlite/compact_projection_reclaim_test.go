package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func seedProjectionFamilies(t *testing.T, dbPath string) (terminalRows int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water,failure_class,terminal_at)
		VALUES('gen-complete','complete','h',0,1,'',''),
		      ('gen-term','failed','h',0,1,'oversize_row','2026-08-01T00:00:00Z');
		UPDATE search_projection_state
		   SET generation_id='gen-complete', active_generation_id='gen-complete', state='complete', phase='complete', config_hash='h'
		 WHERE singleton=1;
		INSERT INTO search_projection_source_sequence(event_id) VALUES('c1'),('c2'),('t1'),('t2'),('t3');
		INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version)
		VALUES('gen-complete','s','keep-a',1,1),
		      ('gen-complete','s','keep-b',1,1),
		      ('gen-term','s','drop-a',1,1),
		      ('gen-term','s','drop-b',1,1),
		      ('gen-term','s','drop-c',1,1);
		INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version)
		SELECT 'gen-complete', sequence, event_id, randomblob(16), 1 FROM search_projection_source_sequence WHERE event_id IN ('c1','c2');
		INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version)
		SELECT 'gen-term', sequence, event_id, randomblob(16), 1 FROM search_projection_source_sequence WHERE event_id IN ('t1','t2','t3');
	`); err != nil {
		t.Fatal(err)
	}
	return 6
}

func TestCompactReclaimsTerminalProjectionGenerationsOnWorkCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "traceary.db")
	migrations := onDiskSQLiteMigrations(t)
	database := infra.NewDatabase(dbPath, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	wantRows := seedProjectionFamilies(t, dbPath)

	svc := usecase.NewStoreCompactionUsecase(
		dbPath,
		&infra.CompactionFileJournal{Dir: filepath.Join(dir, ".traceary-compaction")},
		&infra.SQLiteCompactionBuilder{},
		infra.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		infra.StoreLeaseCoordinator{},
	)
	result, err := svc.Compact(ctx, application.CompactInput{Source: dbPath, Force: true, Now: time.Now()})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	step, ok := result.Steps.Find(application.CompactStepProjectionReclaim)
	if !ok {
		t.Fatalf("steps=%v, want projection_reclaim", result.Steps)
	}
	if step.Rows != wantRows {
		t.Fatalf("projection_reclaim.rows=%d, want %d", step.Rows, wantRows)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var term, keep int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords WHERE generation_id='gen-term'`).Scan(&term); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords WHERE generation_id='gen-complete'`).Scan(&keep); err != nil {
		t.Fatal(err)
	}
	if term != 0 {
		t.Fatalf("terminal keywords=%d, want 0", term)
	}
	if keep != 2 {
		t.Fatalf("complete keywords=%d, want 2", keep)
	}
}

func TestVerifyPairRejectsCandidateMissingActiveGenerationRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	migrations := onDiskSQLiteMigrations(t)
	if err := infra.NewStoreManagementDatasource(infra.NewDatabase(source, migrations)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	seedProjectionFamilies(t, source)
	candidate := filepath.Join(dir, "candidate.db")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DROP TABLE IF EXISTS event_search_documents;
		DROP TABLE IF EXISTS event_search_fts;
		DROP TABLE IF EXISTS event_search_backfill_state;
		DELETE FROM search_projection_session_keywords WHERE generation_id='gen-complete';
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	err = (infra.SQLiteCompactionBuilder{}).VerifyPair(ctx, source, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of missing active generation rows")
	}
	if !strings.Contains(err.Error(), "gen-complete") {
		t.Fatalf("VerifyPair() error = %v, want generation named", err)
	}
}

func TestCompactInPlaceReportsProjectionReclaimStep(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "traceary.db")
	if err := infra.NewStoreManagementDatasource(infra.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	wantRows := seedProjectionFamilies(t, dbPath)
	var steps application.CompactSteps
	filter := application.CompactFilter{
		OnStep: func(step application.CompactStep) { steps = append(steps, step) },
	}
	if err := (infra.SQLiteCompactionBuilder{}).CompactInPlace(ctx, dbPath, filter); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	step, ok := steps.Find(application.CompactStepProjectionReclaim)
	if !ok {
		t.Fatalf("steps=%v, want projection_reclaim", steps)
	}
	if step.Rows != wantRows {
		t.Fatalf("rows=%d, want %d", step.Rows, wantRows)
	}
}
