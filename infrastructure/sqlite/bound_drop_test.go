package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestEmptyLineageStoreReportJSONUnchangedAfterDrop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 79)
	insertFinalizedUsage(t, path, "usage-empty-lineage", "codex", "openai", "gpt-5.6")

	before := reportUsageJSON(t, path, onDiskSQLiteMigrationsBefore(t, 79))
	var snapshot apptypes.ReportUsageSnapshot
	if err := json.Unmarshal(before, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runs) != 0 {
		t.Fatalf("precondition runs = %d, want []", len(snapshot.Runs))
	}
	if strings.Contains(string(before), `"repository"`) || strings.Contains(string(before), `"ticket"`) ||
		strings.Contains(string(before), `"pull_request"`) || strings.Contains(string(before), `"batch"`) ||
		strings.Contains(string(before), `"packet_bytes"`) || strings.Contains(string(before), `"tool_output_bytes"`) {
		t.Fatalf("precondition JSON leaked retired keys: %s", before)
	}

	if err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	after := reportUsageJSON(t, path, onDiskSQLiteMigrations(t))
	if string(before) != string(after) {
		t.Fatalf("JSON snapshot changed:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestNonEmptyLineageOpenWithoutApprovalLeavesLiveStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 79)
	insertLineageRow(t, path, "codex", "run-keep")
	before := fileDigest(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var required *apptypes.BoundDropApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("Initialize error = %v, want BoundDropApprovalRequiredError", err)
	}
	if required.RowCount != 1 || required.Digest == "" {
		t.Fatalf("preflight = %+v", required)
	}
	if !strings.Contains(required.Error(), "0.48.2") || !strings.Contains(required.Error(), "1:") {
		t.Fatalf("preflight text = %q", required.Error())
	}
	if fileDigest(t, path) != before {
		t.Fatal("live store changed without approval")
	}
}

func TestApprovedDropRemovesTableAndKeepsObservationRuns(t *testing.T) {
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
	seedLegacyStore(t, path, 79)
	insertSourceEvent(t, path, "e-keep")
	insertLineageAndAttribution(t, path, "codex", "run-approved", "usage-approved")
	inspection, err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).InspectBoundDrop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.RowCount != 1 {
		t.Fatalf("inspection count = %d", inspection.RowCount)
	}
	approval, err := domain.NewBoundDropApproval(inspection.RowCount, inspection.Digest)
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
		Operation:         domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:        path,
		ConsumerBinding:   "bound-drop",
		BoundDropApproval: &approval,
		Budget:            upgradeBudget(uint64(info.Size())),
	}); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var tableCount, runCount, minReader int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='run_lineages'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatal("run_lineages still present")
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM usage_observation_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("usage_observation_runs = %d, want 1", runCount)
	}
	if err = db.QueryRow(`SELECT minimum_reader_version FROM store_format_state WHERE singleton=1`).Scan(&minReader); err != nil {
		t.Fatal(err)
	}
	if minReader != 37 {
		t.Fatalf("minimum_reader_version = %d, want 37", minReader)
	}

	usageJSON := reportUsageJSON(t, path, onDiskSQLiteMigrations(t))
	if strings.Contains(string(usageJSON), `"repository"`) || strings.Contains(string(usageJSON), `"packet_bytes"`) ||
		strings.Contains(string(usageJSON), `"tool_output_bytes"`) || strings.Contains(string(usageJSON), `"ticket"`) ||
		strings.Contains(string(usageJSON), `"pull_request"`) || strings.Contains(string(usageJSON), `"batch"`) {
		t.Fatalf("report JSON still has retired keys: %s", usageJSON)
	}
	if !strings.Contains(string(usageJSON), `"engine":"codex"`) {
		t.Fatalf("observations missing from report: %s", usageJSON)
	}
}

func TestStaleBoundDropApprovalStopsBeforeBuild(t *testing.T) {
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
	seedLegacyStore(t, path, 79)
	insertSourceEvent(t, path, "e-stale")
	insertLineageRow(t, path, "codex", "run-stale")
	inspection, err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).InspectBoundDrop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := domain.NewBoundDropApproval(inspection.RowCount, inspection.Digest)
	if err != nil {
		t.Fatal(err)
	}
	insertLineageRow(t, path, "codex", "run-extra")

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
		Operation:         domain.PreparedStoreUpgradeOperationOfflineMigrationUpgrade,
		TargetPath:        path,
		ConsumerBinding:   "bound-drop-stale",
		BoundDropApproval: &approval,
		Budget:            upgradeBudget(uint64(info.Size())),
	})
	var required *apptypes.BoundDropApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("RunUpgrade error = %v, want stale approval", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var tableCount int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='run_lineages'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatal("live store dropped the table after stale approval")
	}
}

func TestPayloadBackfillRemainsPresent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='payload_backfill_runs'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("payload_backfill_runs missing after 079")
	}
}

func insertFinalizedUsage(t *testing.T, path, observationID, host, provider, model string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	observedAt := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO usage_observations (
		observation_id, session_id, host, source_name, source_version, provider, model,
		scope, accounting, status, observed_at, finalized_at, terminal_code,
		input_state, input_tokens, cached_input_state, cached_input_tokens,
		cache_write_input_state, cache_write_input_tokens, output_state, output_tokens,
		reasoning_output_state, reasoning_output_tokens, total_state, total_tokens,
		cost_state
	) VALUES (
		?, 'session-1', ?, 'test', '1', ?, ?,
		'call', 'additive', 'finalized', ?, ?, 'success',
		'known', 10, 'unavailable', NULL,
		'unavailable', NULL, 'known', 2,
		'unavailable', NULL, 'known', 12,
		'unavailable'
	)`, observationID, host, provider, model, observedAt, observedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func insertLineageRow(t *testing.T, path, host, runID string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`INSERT INTO run_lineages (host, run_id) VALUES (?, ?)`, host, runID)
	if err != nil {
		t.Fatal(err)
	}
}

func insertLineageAndAttribution(t *testing.T, path, host, runID, observationID string) {
	t.Helper()
	insertFinalizedUsage(t, path, observationID, host, "openai", "gpt-5.6")
	insertLineageRow(t, path, host, runID)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`INSERT INTO usage_observation_runs (observation_id, run_host, run_id) VALUES (?, ?, ?)`, observationID, host, runID)
	if err != nil {
		t.Fatal(err)
	}
}

func reportUsageJSON(t *testing.T, path string, migrations fs.FS) []byte {
	t.Helper()
	criteria, err := apptypes.ReportCriteriaFrom("2026-07-01", "2026-08-01", "UTC", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := usecase.NewReportUsecase(sqlite.NewReportDatasource(sqlite.NewDatabase(path, migrations))).Generate(context.Background(), criteria)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot.Usage)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
