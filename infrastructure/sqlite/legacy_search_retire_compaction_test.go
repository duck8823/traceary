package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
)

// TestCompactionRefusesAStoreThatStillCarriesTheLegacySearchFamily asserts the
// plan-time guard. Compacting first would copy the dead index into the
// candidate and bake it into the new file, so the operator is told to retire
// it first — by name, since that is the command that unblocks them.
func TestCompactionRefusesAStoreThatStillCarriesTheLegacySearchFamily(t *testing.T) {
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
	_, err := planner.Plan(ctx, source)
	if err == nil {
		t.Fatal("Plan() error = nil, want a refusal while the legacy search family is present")
	}
	if !strings.Contains(err.Error(), "search-retire") {
		t.Fatalf("Plan() error = %v, want it to name `store search-retire`", err)
	}
}

// TestRequireStaticSearchStateGuardsCandidateConstruction pins the second,
// deeper check. Candidate construction runs behind the exclusive lease and
// re-asks the question the plan already asked, because a run planned by an
// older binary carries no plan-time verdict at all. It is asserted directly:
// end to end the source-identity guard fires first, so this one is defence in
// depth rather than a reachable operator path.
func TestRequireStaticSearchStateGuardsCandidateConstruction(t *testing.T) {
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
	if !strings.Contains(err.Error(), "search-retire") {
		t.Fatalf("requireStaticSearchState() error = %v, want it to name `store search-retire`", err)
	}
}

// TestRejectRetiredSearchIndexInspectsWhateverWouldBePublished pins which file
// the pre-exchange guard reads. A run resumed from CandidateVerified has a
// built candidate and never revisits Plan or Build, so a clean source proves
// nothing — only the candidate shows what the exchange would publish.
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
	if !strings.Contains(err.Error(), "search-retire") {
		t.Fatalf("RejectRetiredSearchIndex() error = %v, want it to name `store search-retire`", err)
	}

	// A prepared-migration publication is how a store reaches the schema where
	// the family can be retired at all; refusing it would make the family
	// unremovable on exactly the stores that carry it.
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

// addLegacySearchFamilyObject reproduces what the guard actually looks for: a
// named member of the migration-032 family in the schema. Its contents are
// irrelevant to the check, so the fixture stays a plain table.
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
