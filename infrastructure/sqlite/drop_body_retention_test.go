package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestDropBodyRetention_HistoricalMigrationsByteUnchanged(t *testing.T) {
	t.Parallel()
	dir := onDiskSQLiteMigrationDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") || name == "000083_drop_body_retention.sql" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(got)
		digest := hex.EncodeToString(sum[:])
		if digest == "" {
			t.Fatalf("%s hashed empty", name)
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, "000026_add_raw_body_retention.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "body_availability") {
		t.Fatal("historical 000026 no longer introduces body_availability")
	}
}

func TestDropBodyRetention_LiveOpenLeavesPopulatedStoreUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 83)
	insertUnavailableRetentionEvent(t, path, "evt-unavail")
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var required *apptypes.UnavailableRetentionApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want UnavailableRetentionApprovalRequiredError", err)
	}
	if required.RowCount != 1 || !strings.Contains(required.Error(), "0.48.2") || !strings.Contains(required.Error(), "cannot be recovered") {
		t.Fatalf("live-open preflight = %+v error=%q", required, err)
	}
	after := readStoreBytes(t, path)
	if string(after) != string(before) {
		t.Fatal("live open mutated the populated store")
	}
}

func TestDropBodyRetention_UnapprovedUpgradeStopsAndLeavesLiveStore(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
	seedLegacyStore(t, path, 83)
	insertUnavailableRetentionEvent(t, path, "evt-unavail")
	before := fileDigest(t, path)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(path, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:       domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:      path,
		ConsumerBinding: "drop-body-retention",
		Budget:          upgradeBudget(uint64(info.Size())),
	})
	var required *apptypes.UnavailableRetentionApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("RunUpgrade error = %v, want UnavailableRetentionApprovalRequiredError", err)
	}
	if required.RowCount != 1 || required.Digest == "" {
		t.Fatalf("preflight = %+v", required)
	}
	if !strings.Contains(required.Error(), "0.48.2") || !strings.Contains(required.Error(), "cannot be recovered") {
		t.Fatalf("preflight text = %q", required.Error())
	}
	if fileDigest(t, path) != before {
		t.Fatal("live store changed without approval")
	}
}

func TestDropBodyRetention_ApprovedUpgradeDropsColumnAndTables(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
	seedLegacyStore(t, path, 83)
	insertUnavailableRetentionEvent(t, path, "evt-unavail")
	inspection, err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).InspectUnavailableRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.RowCount != 1 {
		t.Fatalf("inspection count = %d", inspection.RowCount)
	}
	approval, err := domain.NewUnavailableRetentionApproval(inspection.RowCount, inspection.Digest, inspection.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(path, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	if _, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:                    domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:                   path,
		ConsumerBinding:              "drop-body-retention-approved",
		UnavailableRetentionApproval: &approval,
		Budget:                       upgradeBudget(uint64(info.Size())),
	}); err != nil {
		t.Fatal(err)
	}

	assertMinimumReaderVersion(t, path, 39)
	assertQuickCheckOK(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var hasColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'body_availability'`).Scan(&hasColumn); err != nil {
		t.Fatal(err)
	}
	if hasColumn != 0 {
		t.Fatal("body_availability still present")
	}
	for _, name := range []string{"raw_body_retention_entries", "raw_body_retention_executions", "raw_body_retention_store_identity", "session_orphan_ranges"} {
		if tablePresent(t, path, name) {
			t.Fatalf("%s survived 083", name)
		}
	}
	assertEventBody(t, path, "evt-unavail", types.EventBodyUnavailableRetentionMarker)
}

func TestDropBodyRetention_StaleApprovalLeavesLiveStore(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
	seedLegacyStore(t, path, 83)
	insertUnavailableRetentionEvent(t, path, "evt-stale")
	inspection, err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).InspectUnavailableRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := domain.NewUnavailableRetentionApproval(inspection.RowCount, inspection.Digest, inspection.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	insertUnavailableRetentionEvent(t, path, "evt-extra")
	before := fileDigest(t, path)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := &sqlite.PreparedStoreUpgradeFileJournal{Dir: filepath.Join(dir, "journal")}
	recipe := &sqlite.PreparedUpgradeMigrationRecipe{PreparedMigrationCandidateRecipe: sqlite.PreparedMigrationCandidateRecipe{
		Migrations: all,
		Verifier:   sqlite.PreparedMigrationVerifier{Migrations: all},
	}}
	svc := usecase.NewPreparedStoreUpgradeUsecase(path, journal, sqlite.NewPreparedStoreUpgradeFilesForTest(true, nil), sqlite.StoreLeaseCoordinator{}, map[domain.PreparedStoreUpgradeOperation]application.PreparedCandidateRecipe{
		domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade: recipe,
	})
	_, err = svc.RunUpgrade(ctx, application.PreparedStoreUpgradeCommand{
		Operation:                    domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:                   path,
		ConsumerBinding:              "drop-body-retention-stale",
		UnavailableRetentionApproval: &approval,
		Budget:                       upgradeBudget(uint64(info.Size())),
	})
	var required *apptypes.UnavailableRetentionApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("RunUpgrade error = %v, want stale approval", err)
	}
	if fileDigest(t, path) != before {
		t.Fatal("live store changed after stale approval")
	}
	var hasColumn int
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'body_availability'`).Scan(&hasColumn); err != nil {
		t.Fatal(err)
	}
	if hasColumn != 1 {
		t.Fatal("live store dropped body_availability after stale approval")
	}
}

func insertUnavailableRetentionEvent(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(
		`INSERT INTO events(id, kind, agent, session_id, body, created_at, client, workspace, body_availability)
		 VALUES (?, 'prompt', 'codex', 's', ?, '2026-04-01T00:00:00Z', 'hook', 'w', 'unavailable_retention')`,
		id,
		types.EventBodyUnavailableRetentionMarker,
	)
	if err != nil {
		t.Fatal(err)
	}
}
