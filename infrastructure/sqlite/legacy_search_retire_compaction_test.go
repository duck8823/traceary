package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
)

func TestCompactionDropsTheLegacySearchFamilyDuringTheCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	createCompactableStore(t, source)
	addLegacySearchFamilyObject(t, source)

	planner := usecase.NewStoreCompactionUsecase(
		source,
		&CompactionFileJournal{Dir: filepath.Join(dir, "planning-journal")},
		SQLiteCompactionBuilder{},
		StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		StoreLeaseCoordinator{},
	)
	run, err := planner.Plan(ctx, source)
	if err != nil {
		t.Fatalf("Plan() error = %v, want success so compact can drop the family", err)
	}
	journal := &CompactionFileJournal{Dir: filepath.Join(dir, "apply-journal")}
	if err := journal.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := usecase.NewStoreCompactionUsecase(
		source,
		journal,
		SQLiteCompactionBuilder{},
		StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		StoreLeaseCoordinator{},
	)
	if _, err := service.Apply(ctx, run.ID); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	present, err := legacySearchFamilyPresent(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("compacted store still carries the retired search family")
	}
}

func TestRequireStaticSearchStateReportsFamilyWithoutSearchRetire(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	createCompactableStore(t, source)

	retired, err := openDirectReadOnly(ctx, source)
	if err != nil {
		t.Fatalf("openDirectReadOnly() error = %v", err)
	}
	if err := requireStaticSearchState(ctx, retired); err != nil {
		t.Fatalf("requireStaticSearchState() on a retired store error = %v", err)
	}
	if err := retired.Close(); err != nil {
		t.Fatalf("close retired-store handle: %v", err)
	}

	addLegacySearchFamilyObject(t, source)
	carrying, err := openDirectReadOnly(ctx, source)
	if err != nil {
		t.Fatalf("openDirectReadOnly() error = %v", err)
	}
	defer func() { _ = carrying.Close() }()
	err = requireStaticSearchState(ctx, carrying)
	if err == nil {
		t.Fatal("requireStaticSearchState() error = nil, want a refusal while the family is present")
	}
	if strings.Contains(err.Error(), "search-retire") {
		t.Fatalf("requireStaticSearchState() error = %v, must not name the removed command", err)
	}
}

func TestRejectRetiredSearchIndexInspectsWhateverWouldBePublished(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	candidate := filepath.Join(dir, "candidate.db")
	createCompactableStore(t, source)
	createCompactableStore(t, candidate)
	addLegacySearchFamilyObject(t, candidate)

	run := domain.CompactionRun{SourcePath: source, CandidatePath: candidate}
	err := PreparedStoreUpgradeFiles{}.RejectRetiredSearchIndex(ctx, run)
	if err == nil {
		t.Fatal("RejectRetiredSearchIndex() error = nil, want a refusal for a candidate carrying the family")
	}
	if strings.Contains(err.Error(), "search-retire") {
		t.Fatalf("RejectRetiredSearchIndex() error = %v, must not name the removed command", err)
	}

	rehearsal := run
	rehearsal.Operation = domain.PreparedStoreUpgradeOperationPayloadRehearsalMigration
	if err := (PreparedStoreUpgradeFiles{}).RejectRetiredSearchIndex(ctx, rehearsal); err != nil {
		t.Fatalf("RejectRetiredSearchIndex() on a prepared migration error = %v, want it exempt", err)
	}
}

func createCompactableStore(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(path))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sample(id INTEGER PRIMARY KEY, body BLOB); INSERT INTO sample(body) VALUES(zeroblob(1048576))`); err != nil {
		t.Fatalf("seed compactable store: %v", err)
	}
}

func addLegacySearchFamilyObject(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(path))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE event_search_documents(event_id TEXT PRIMARY KEY, body_text TEXT NOT NULL DEFAULT '', command_text TEXT NOT NULL DEFAULT '', input_text TEXT NOT NULL DEFAULT '', output_text TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create legacy search family object: %v", err)
	}
}

func TestInspectBodyGateZeroOnSampleStore(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.db")
	createCompactableStore(t, source)
	gate, err := (SQLiteCompactionBuilder{}).InspectBodyGate(ctx, source, application.CompactCutoff(time.Now(), 90))
	if err != nil {
		t.Fatal(err)
	}
	if gate.MustRefuse(false) {
		t.Fatalf("sample store gate = %+v, must not refuse", gate)
	}
}
