package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestDropCompatSurface_HistoricalMigrationsByteUnchanged(t *testing.T) {
	t.Parallel()
	dir := onDiskSQLiteMigrationDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000034_create_event_metadata_projection.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "legacy_source_hook") {
		t.Fatal("historical 000034 no longer introduces legacy_source_hook")
	}
}

func TestDropCompatSurface_LiveOpenLeavesNonNullStoreUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 86)
	insertLegacySourceHookRow(t, path)
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var refused *apptypes.LegacyHookNonEmptyError
	if !errors.As(err, &refused) {
		t.Fatalf("error=%v, want LegacyHookNonEmptyError", err)
	}
	if refused.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", refused.RowCount)
	}
	msg := refused.Error()
	if !strings.Contains(msg, "1 non-null") || !strings.Contains(msg, "0.48.2") {
		t.Fatalf("refuse text = %q", msg)
	}
	after := readStoreBytes(t, path)
	if string(after) != string(before) {
		t.Fatal("live open mutated the non-null legacy_source_hook store")
	}
}

func TestDropCompatSurface_UpgradeRefusesNonNullWithoutDropping(t *testing.T) {
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
	seedLegacyStore(t, path, 86)
	insertLegacySourceHookRow(t, path)
	insertSourceEvent(t, path, "evt-keep")
	before := fileDigest(t, path)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	err = runUpgradeExpectError(ctx, t, dir, path, all)
	var refused *apptypes.LegacyHookNonEmptyError
	if !errors.As(err, &refused) {
		t.Fatalf("error=%v, want LegacyHookNonEmptyError", err)
	}
	if refused.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", refused.RowCount)
	}
	if fileDigest(t, path) != before {
		t.Fatal("live store changed on refuse")
	}
	if !columnPresent(t, path, "event_metadata_projection", "legacy_source_hook") {
		t.Fatal("legacy_source_hook dropped despite refuse")
	}
}

func TestDropCompatSurface_EmptyColumnUpgradeDropsAndRaisesReader(t *testing.T) {
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
	seedLegacyStore(t, path, 86)
	insertSourceEvent(t, path, "evt-keep")
	if !columnPresent(t, path, "event_metadata_projection", "legacy_source_hook") {
		t.Fatal("fixture missing legacy_source_hook")
	}

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, path, all)
	if columnPresent(t, path, "event_metadata_projection", "legacy_source_hook") {
		t.Fatal("legacy_source_hook survived empty drop")
	}
	assertMinimumReaderVersion(t, path, 42)
	assertRecreatedProjectionTriggers(t, path)
}

func TestDropCompatSurface_LiveOpenDefersEmptyPopulatedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 86)
	insertSourceEvent(t, path, "evt-keep")
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	found := false
	for _, version := range required.Versions {
		if version == 86 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending = %v, want 86", required.Versions)
	}
	if string(readStoreBytes(t, path)) != string(before) {
		t.Fatal("live open applied 086 inline on a populated store")
	}
	if !columnPresent(t, path, "event_metadata_projection", "legacy_source_hook") {
		t.Fatal("live open dropped legacy_source_hook")
	}
}

func insertLegacySourceHookRow(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO event_metadata_projection (
		id, kind, client, agent, session_id, workspace, source_hook, legacy_source_hook,
		created_at, created_at_norm, body_original_bytes, body_stored_bytes,
		body_ingest_truncated, body_storage_truncated, body_metadata_version
	) VALUES (
		'legacy-hook-row', 'session_ended', 'cli', 'codex', 's1', '/repo', NULL, 'subagent_stop',
		'2026-04-01T00:00:00Z', '2026-04-01T00:00:00.000000000Z', 0, 0, 0, 0, 1
	)`)
	if err != nil {
		t.Fatal(err)
	}
}

func columnPresent(t *testing.T, path, table, column string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func assertRecreatedProjectionTriggers(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, name := range []string{
		"event_metadata_projection_events_after_insert",
		"event_metadata_projection_events_after_update",
	} {
		var sqlText string
		if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`, name).Scan(&sqlText); err != nil {
			t.Fatalf("missing trigger %s: %v", name, err)
		}
		if strings.Contains(sqlText, "legacy_source_hook") {
			t.Fatalf("trigger %s still names legacy_source_hook", name)
		}
	}
	var leftover int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE sql LIKE '%legacy_source_hook%'`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("sqlite_schema still names legacy_source_hook (%d objects)", leftover)
	}
}
