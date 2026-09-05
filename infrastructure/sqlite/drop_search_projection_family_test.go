package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

var droppedSearchFamilyNames = []string{
	"literal_search_fingerprints",
	"literal_search_projection_state",
	"search_projection_command_aggregates",
	"search_projection_exclusions",
	"search_projection_generation_lifecycle",
	"search_projection_inventory_compat",
	"search_projection_inventory_state",
	"search_projection_recent_documents",
	"search_projection_session_keywords",
	"search_projection_session_summaries",
	"search_projection_source_revision",
	"search_projection_source_sequence",
	"search_projection_state",
	"search_projection_recent_fts",
}

var droppedSearchFamilyTriggers = []string{
	"literal_search_event_insert",
	"literal_search_event_update",
	"literal_search_event_delete",
	"literal_search_audit_insert",
	"literal_search_audit_update",
	"literal_search_audit_delete",
	"search_projection_events_insert",
	"search_projection_events_update",
	"search_projection_events_delete",
	"search_projection_audits_insert",
	"search_projection_audits_update",
	"search_projection_audits_delete",
	"search_projection_complete_event_update",
	"search_projection_complete_event_delete",
	"search_projection_complete_audit_insert",
	"search_projection_complete_audit_update",
	"search_projection_complete_audit_delete",
}

func TestDropSearchProjectionFamily_OfflineCandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 80)
	insertSourceEvent(t, target, "e-drop")
	assertSearchFamilyPresent(t, target, true)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	receipt := runUpgradeOn(ctx, t, dir, target, all)
	if receipt.RunID == "" {
		t.Fatal("empty receipt")
	}
	assertSearchFamilyPresent(t, target, false)
	assertDroppedTriggersAbsent(t, target)
	assertEventMetadataProjectionPresent(t, target)
	assertMinimumReaderVersion(t, target, 39)
	assertSchemaMigrationSuffix(t, target, 80, "000080_drop_search_projection_family.sql")
	assertPostUpgradeWritesSucceed(t, target)
}

func TestEventMetadataProjection_SurvivesUpgrade(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 80)
	insertSourceEvent(t, target, "e-meta")
	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)

	db := sqlite.NewDatabase(target, onDiskSQLiteMigrations(t))
	if _, err := sqlite.NewCapacityInspector(db).InspectCapacity(ctx); err != nil {
		t.Fatalf("InspectCapacity() error = %v", err)
	}
	if _, err := sqlite.NewOperatorCostInspector(db).InspectOperatorCost(ctx, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 0); err != nil {
		t.Fatalf("InspectOperatorCost() error = %v", err)
	}
	fold, err := sqlite.NewFoldGateInspector(db).InspectFoldGate(ctx, 0, 0)
	if err != nil {
		t.Fatalf("InspectFoldGate() error = %v", err)
	}
	if fold.Evidence.Reason == "required fold-gate tables unavailable" {
		t.Fatalf("fold-gate lost event_metadata_projection: %+v", fold)
	}
	assertEventMetadataProjectionPresent(t, target)
}

func TestDropSearchProjectionFamily_LiveOpenLeavesInodeUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 80)
	insertSourceEvent(t, target, "e-live")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	current := newStoreManagementDatasource(t, target, onDiskSQLiteMigrations(t))
	if err := current.Initialize(ctx); err == nil {
		t.Fatal("Initialize() error = nil, want offline-migrations required")
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("live open mutated the store inode")
	}
	assertSearchFamilyPresent(t, target, true)
}

func TestSearchCanonicalMembershipMatchesTwoTierFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(path, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	events := sqlite.NewEventDatasource(database)
	insertSourceEvent(t, path, "evt-alpha")
	insertSourceEvent(t, path, "evt-beta")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE events SET body='needle in alpha' WHERE id='evt-alpha'`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE events SET body='other beta' WHERE id='evt-beta'`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	page, err := events.SearchTwoTier(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchTwoTier() error = %v", err)
	}
	full, err := events.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	metadata, err := events.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if len(page.Events()) != 1 || page.Events()[0].Event().EventID().String() != "evt-alpha" {
		t.Fatalf("two-tier events = %+v", page.Events())
	}
	if len(full) != 1 || full[0].EventID().String() != "evt-alpha" {
		t.Fatalf("SearchPage events = %+v", full)
	}
	if len(metadata) != 1 || metadata[0].EventID().String() != "evt-alpha" {
		t.Fatalf("SearchMetadata = %+v", metadata)
	}

	filterOnly := apptypes.NewEventSearchCriteriaBuilder(10).Build()
	structural, err := events.SearchPage(ctx, filterOnly)
	if err != nil {
		t.Fatalf("filter-only SearchPage() error = %v", err)
	}
	if len(structural) != 2 {
		t.Fatalf("filter-only SearchPage len = %d, want 2", len(structural))
	}
}

func TestDropSearchProjectionFamily_RealSizedOperatorCopy(t *testing.T) {
	source := strings.TrimSpace(os.Getenv("TRACEARY_OPERATOR_STORE"))
	if source == "" {
		t.Skip("set TRACEARY_OPERATOR_STORE to run the real-sized operator copy")
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatalf("stat operator store: %v", err)
	}
	if info.Size() < 64<<20 {
		t.Skipf("operator store %s is %d bytes, below real-sized threshold", source, info.Size())
	}
	if testing.Short() {
		t.Skip("real-sized operator copy")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}

	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "operator-copy.db")
	progressf(t, "locked snapshot copy of live store (%d bytes)", info.Size())
	if err := consistentCopyStore(source, target); err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes := copyInfo.Size()
	progressf(t, "copy complete (%d bytes); counting conserved tables", beforeBytes)
	beforeCounts := fiveTableCounts(t, target)
	progressf(t, "five-table counts before=%+v; starting offline upgrade", beforeCounts)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	receipt := runUpgradeOnWithBudget(ctx, t, dir, target, all, realSizedUpgradeBudget(uint64(beforeBytes)))
	afterInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	progressf(t, "operator copy shrink: before=%d after=%d delta=%d run_id=%s", beforeBytes, afterInfo.Size(), beforeBytes-afterInfo.Size(), receipt.RunID)
	logProjectionMaster(t, target)
	assertSearchFamilyPresent(t, target, false)
	assertEventMetadataProjectionPresent(t, target)
	assertQuickCheckOK(t, target)
	afterCounts := fiveTableCounts(t, target)
	if beforeCounts != afterCounts {
		t.Fatalf("five-table counts changed: before=%+v after=%+v", beforeCounts, afterCounts)
	}
	progressf(t, "five-table counts after=%+v quick_check=ok", afterCounts)
}

func assertSearchFamilyPresent(t *testing.T, path string, want bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, name := range droppedSearchFamilyNames {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if want && count == 0 {
			t.Fatalf("expected %s present", name)
		}
		if !want && count != 0 {
			t.Fatalf("expected %s absent, count=%d", name, count)
		}
	}
}

func assertDroppedTriggersAbsent(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, name := range droppedSearchFamilyTriggers {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("trigger %s survived DROP", name)
		}
	}
}

func assertEventMetadataProjectionPresent(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='event_metadata_projection'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("event_metadata_projection missing")
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM event_metadata_projection LIMIT 1`).Scan(&id); err != nil && err != sql.ErrNoRows {
		t.Fatalf("event_metadata_projection read: %v", err)
	}
}

func assertMinimumReaderVersion(t *testing.T, path string, want int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got int
	if err := db.QueryRow(`SELECT minimum_reader_version FROM store_format_state WHERE singleton=1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("minimum_reader_version = %d, want %d", got, want)
	}
}

func assertSchemaMigrationSuffix(t *testing.T, path string, version int64, name string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var gotName string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version=?`, version).Scan(&gotName); err != nil {
		t.Fatal(err)
	}
	if gotName != name {
		t.Fatalf("schema_migrations(%d)=%q, want %q", version, gotName, name)
	}
}

func assertPostUpgradeWritesSucceed(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO events(id, kind, agent, session_id, body, created_at, client, workspace) VALUES('evt-post','note','codex','sess-post','body',?,'cli','ws')`, now); err != nil {
		t.Fatalf("INSERT INTO events after upgrade: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO command_audits(event_id, command_text, input_text, output_text) VALUES('evt-post','echo hi','','')`); err != nil {
		t.Fatalf("INSERT INTO command_audits after upgrade: %v", err)
	}
}

func assertQuickCheckOK(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("PRAGMA quick_check = %q, want ok", result)
	}
}

func progressf(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	t.Log(msg)
	fmt.Fprintf(os.Stderr, "real-sized: %s\n", msg)
}

func realSizedUpgradeBudget(size uint64) domain.PreparedStoreUpgradeBudget {
	if size < 1<<20 {
		size = 1 << 20
	}
	return domain.PreparedStoreUpgradeBudget{
		WallTimeLimit:      4 * time.Hour,
		PublishLockLimit:   4 * time.Hour,
		OwnedDiskByteLimit: size*16 + 1<<30,
		WALByteLimit:       size*4 + 1<<30,
		TemporaryByteLimit: size*8 + 1<<30,
		SafetyMarginBytes:  64 << 20,
	}
}

type fiveTableSnapshot struct {
	Events, Sessions, Audits, Memories, Refinements int64
}

func consistentCopyStore(src, dst string) error {
	ctx := context.Background()
	db, err := sql.Open("sqlite", src)
	if err != nil {
		return fmt.Errorf("open source for consistent copy: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin source connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=120000`); err != nil {
		return fmt.Errorf("set busy_timeout for consistent copy: %w", err)
	}
	// Exclusive lock freezes writers and checkpoints so the main file plus
	// WAL sidecars are one snapshot. VACUUM INTO on the live 12GiB store
	// restarted under hook writes and never finished.
	if _, err := conn.ExecContext(ctx, `BEGIN EXCLUSIVE`); err != nil {
		return fmt.Errorf("begin exclusive snapshot: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if err := copyFileStreaming(src, dst); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(src + suffix); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", src+suffix, err)
		}
		if err := copyFileStreaming(src+suffix, dst+suffix); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit exclusive snapshot: %w", err)
	}
	committed = true
	return checkpointCopyForClone(dst)
}

func checkpointCopyForClone(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open copy for checkpoint: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = db.Close()
		return fmt.Errorf("checkpoint copy: %w", err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		_ = db.Close()
		return fmt.Errorf("disable copy WAL: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close copy after checkpoint: %w", err)
	}
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	return nil
}

func copyFileStreaming(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy store: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync destination: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}
	return nil
}

func logProjectionMaster(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE name IN ('literal_search_fingerprints','literal_search_projection_state','search_projection_command_aggregates','search_projection_exclusions','search_projection_generation_lifecycle','search_projection_inventory_compat','search_projection_inventory_state','search_projection_recent_documents','search_projection_session_keywords','search_projection_session_summaries','search_projection_source_revision','search_projection_source_sequence','search_projection_state','search_projection_recent_fts') ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	progressf(t, "sqlite_master family names=%q (want empty)", names)
	var kept string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE name='event_metadata_projection'`).Scan(&kept); err != nil {
		t.Fatalf("event_metadata_projection: %v", err)
	}
	progressf(t, "sqlite_master event_metadata_projection=%q", kept)
}

func fiveTableCounts(t *testing.T, path string) fiveTableSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	count := func(table string) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	return fiveTableSnapshot{
		Events:      count("events"),
		Sessions:    count("sessions"),
		Audits:      count("command_audits"),
		Memories:    count("memories"),
		Refinements: count("session_refinements"),
	}
}
