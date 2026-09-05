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

func TestDropArchiveSegments_HistoricalMigrationsByteUnchanged(t *testing.T) {
	t.Parallel()
	dir := onDiskSQLiteMigrationDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "000044_add_archive_segment_catalog.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "CREATE TABLE archive_segments") {
		t.Fatal("historical 000044 no longer introduces archive_segments")
	}
}

func TestDropArchiveSegments_LiveOpenLeavesNonEmptyStoreUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 84)
	insertArchiveSegmentRow(t, path)
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var refused *apptypes.ArchiveSegmentsNonEmptyError
	if !errors.As(err, &refused) {
		t.Fatalf("error=%v, want ArchiveSegmentsNonEmptyError", err)
	}
	if refused.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", refused.RowCount)
	}
	msg := refused.Error()
	if !strings.Contains(msg, "1 row") || !strings.Contains(msg, "0.48.2") || !strings.Contains(msg, "archive-restore") || !strings.Contains(msg, "bundle export") {
		t.Fatalf("refuse text = %q", msg)
	}
	after := readStoreBytes(t, path)
	if string(after) != string(before) {
		t.Fatal("live open mutated the populated archive_segments store")
	}
}

func TestDropArchiveSegments_UpgradeRefusesNonEmptyWithoutDropping(t *testing.T) {
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
	seedLegacyStore(t, path, 84)
	insertArchiveSegmentRow(t, path)
	insertSourceEvent(t, path, "evt-keep")
	before := fileDigest(t, path)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	err = runUpgradeExpectError(ctx, t, dir, path, all)
	var refused *apptypes.ArchiveSegmentsNonEmptyError
	if !errors.As(err, &refused) {
		t.Fatalf("error=%v, want ArchiveSegmentsNonEmptyError", err)
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
	if !tablePresent(t, path, "archive_segments") {
		t.Fatal("archive_segments dropped despite refuse")
	}
}

func TestDropArchiveSegments_EmptyTableUpgradeDropsAndRaisesReader(t *testing.T) {
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
	seedLegacyStore(t, path, 84)
	insertSourceEvent(t, path, "evt-keep")
	if !tablePresent(t, path, "archive_segments") {
		t.Fatal("fixture missing archive_segments")
	}

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, path, all)
	if tablePresent(t, path, "archive_segments") {
		t.Fatal("archive_segments survived empty drop")
	}
	assertMinimumReaderVersion(t, path, 40)
}

func TestDropArchiveSegments_LiveOpenDefersEmptyPopulatedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 84)
	insertSourceEvent(t, path, "evt-keep")
	before := readStoreBytes(t, path)

	err := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t)).Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	found := false
	for _, version := range required.Versions {
		if version == 84 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending = %v, want 84", required.Versions)
	}
	if string(readStoreBytes(t, path)) != string(before) {
		t.Fatal("live open applied 084 inline on a populated store")
	}
	if !tablePresent(t, path, "archive_segments") {
		t.Fatal("live open dropped archive_segments")
	}
}

func insertArchiveSegmentRow(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	digest := strings.Repeat("ab", 32)
	basename := "segment-v1-" + digest + ".sqlite"
	if len(basename) != 82 {
		t.Fatalf("basename length = %d, want 82", len(basename))
	}
	_, err = db.Exec(`INSERT INTO archive_segments (
		basename, store_id, format_version, start_sequence, end_sequence, unit_count, audit_count,
		min_created_at, max_created_at, time_complete, plain_value_count, zstd_value_count,
		total_plain_bytes, total_stored_bytes, logical_digest, file_digest
	) VALUES (?, 'store-1', 1, 1, 1, 1, 0, '', '', 0, 0, 0, 0, 0, ?, ?)`, basename, digest, digest)
	if err != nil {
		t.Fatal(err)
	}
}
