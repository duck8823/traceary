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

func TestDropMemoryEdges_HistoricalMigrationsByteUnchanged(t *testing.T) {
	t.Parallel()
	dir := onDiskSQLiteMigrationDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000013_create_memory_edges.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "CREATE TABLE memory_edges") {
		t.Fatal("historical 000013 no longer introduces memory_edges")
	}
}

func TestDropMemoryEdges_LiveOpenLeavesNonEmptyStoreUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 85)
	insertMemoryEdgeRow(t, path)
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var refused *apptypes.MemoryEdgesNonEmptyError
	if !errors.As(err, &refused) {
		t.Fatalf("error=%v, want MemoryEdgesNonEmptyError", err)
	}
	if refused.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", refused.RowCount)
	}
	msg := refused.Error()
	if !strings.Contains(msg, "1 row") || !strings.Contains(msg, "0.48.2") || !strings.Contains(msg, "bundle export") {
		t.Fatalf("refuse text = %q", msg)
	}
	after := readStoreBytes(t, path)
	if string(after) != string(before) {
		t.Fatal("live open mutated the populated memory_edges store")
	}
}

func TestDropMemoryEdges_UpgradeRefusesNonEmptyWithoutDropping(t *testing.T) {
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
	seedLegacyStore(t, path, 85)
	insertMemoryEdgeRow(t, path)
	insertSourceEvent(t, path, "evt-keep")
	before := fileDigest(t, path)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	err = runUpgradeExpectError(ctx, t, dir, path, all)
	var refused *apptypes.MemoryEdgesNonEmptyError
	if !errors.As(err, &refused) {
		t.Fatalf("error=%v, want MemoryEdgesNonEmptyError", err)
	}
	if refused.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", refused.RowCount)
	}
	if !strings.Contains(refused.Error(), "0.48.2") {
		t.Fatalf("refuse text = %q", refused.Error())
	}
	if fileDigest(t, path) != before {
		t.Fatal("live store changed on refuse")
	}
	if !tablePresent(t, path, "memory_edges") {
		t.Fatal("memory_edges dropped despite refuse")
	}
}

func TestDropMemoryEdges_EmptyTableUpgradeDropsAndRaisesReader(t *testing.T) {
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
	seedLegacyStore(t, path, 85)
	insertSourceEvent(t, path, "evt-keep")
	if !tablePresent(t, path, "memory_edges") {
		t.Fatal("fixture missing memory_edges")
	}

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, path, all)
	if tablePresent(t, path, "memory_edges") {
		t.Fatal("memory_edges survived empty drop")
	}
	assertMinimumReaderVersion(t, path, 42)
}

func TestDropMemoryEdges_LiveOpenDefersEmptyPopulatedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 85)
	insertSourceEvent(t, path, "evt-keep")
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	found := false
	for _, version := range required.Versions {
		if version == 85 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending = %v, want 85", required.Versions)
	}
	if string(readStoreBytes(t, path)) != string(before) {
		t.Fatal("live open applied 085 inline on a populated store")
	}
	if !tablePresent(t, path, "memory_edges") {
		t.Fatal("live open dropped memory_edges")
	}
}

func insertMemoryEdgeRow(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO memory_edges (
		id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at
	) VALUES (
		'edge-legacy', 'mem-a', 'mem-b', 'related-to',
		'2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z'
	)`)
	if err != nil {
		t.Fatal(err)
	}
}
