package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestEventMetadataQuery_ListRecentDoesNotHydrateBody(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	small := newEventForSQLiteTest(t, "event-small", "cli", "codex", "session-1", "duck8823/traceary", "x", base)
	largeBody := strings.Repeat("large-payload-", 256*1024)
	large := newEventForSQLiteTest(t, "event-large", "cli", "codex", "session-1", "duck8823/traceary", largeBody, base.Add(time.Second))
	for _, event := range []*model.Event{small, large} {
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", event.EventID(), err)
		}
	}

	criteria := apptypes.NewEventListCriteriaBuilder(10).Build()
	got, err := sut.ListRecentMetadata(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ListRecentMetadata() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(metadata) = %d, want 2", len(got))
	}
	if diff := cmp.Diff([]string{"event-large", "event-small"}, metadataIDs(got)); diff != "" {
		t.Fatalf("event order mismatch (-want +got):\n%s", diff)
	}
	if got[0].BodyExtent().StoredBytes() != len(largeBody) {
		t.Fatalf("large StoredBytes() = %d, want %d", got[0].BodyExtent().StoredBytes(), len(largeBody))
	}
	if got[1].BodyExtent().StoredBytes() != 1 {
		t.Fatalf("small StoredBytes() = %d, want 1", got[1].BodyExtent().StoredBytes())
	}

	full, err := sut.ListRecent(context.Background(), 10, 0, types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace(""), false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	fullIDs := make([]string, 0, len(full))
	for _, event := range full {
		fullIDs = append(fullIDs, event.EventID().String())
	}
	if diff := cmp.Diff(fullIDs, metadataIDs(got)); diff != "" {
		t.Fatalf("metadata/full membership mismatch (-want +got):\n%s", diff)
	}
}

func TestEventMetadataQuery_AllocationDoesNotScaleWithStoredBody(t *testing.T) {
	const iterations = 8
	measure := func(name, body string) uint64 {
		t.Helper()
		dbPath := filepath.Join(t.TempDir(), name, "traceary.db")
		sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
		if err := storeManager.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize(%s) error = %v", name, err)
		}
		event := newEventForSQLiteTest(t, "event-1", "cli", "codex", "session-1", "ws", body, time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC))
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", name, err)
		}

		criteria := apptypes.NewEventListCriteriaBuilder(1).Build()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		for range iterations {
			got, err := sut.ListRecentMetadata(context.Background(), criteria)
			if err != nil {
				t.Fatalf("ListRecentMetadata(%s) error = %v", name, err)
			}
			if len(got) != 1 || got[0].BodyExtent().StoredBytes() != len(body) {
				t.Fatalf("ListRecentMetadata(%s) returned invalid extent", name)
			}
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}

	smallAlloc := measure("small", "x")
	largeBody := strings.Repeat("0123456789abcdef", 512*1024) // 8 MiB.
	largeAlloc := measure("large", largeBody)
	const allowedDelta = 512 * 1024
	if largeAlloc > smallAlloc+allowedDelta {
		t.Fatalf("metadata allocation scaled with body: small=%d large=%d delta=%d (allowed %d)", smallAlloc, largeAlloc, largeAlloc-smallAlloc, allowedDelta)
	}
}

func TestEventMetadataQuery_ReturnsBodyFreeCommandAuditMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	event, audit := newSearchAuditFixture(t, "event-audit", "duck8823/traceary", time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC))
	audit.SetExitCode(types.Some(23))
	audit.SetFailed(true)
	if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	got, err := sut.ListRecentMetadata(context.Background(), apptypes.NewEventListCriteriaBuilder(1).FailuresOnly(true).Build())
	if err != nil {
		t.Fatalf("ListRecentMetadata() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(metadata) = %d, want 1", len(got))
	}
	auditMetadata, ok := got[0].CommandAudit().Value()
	if !ok {
		t.Fatal("CommandAudit() absent, want metadata")
	}
	exitCode, ok := auditMetadata.ExitCode().Value()
	if !ok || exitCode != 23 || !auditMetadata.Failed() {
		t.Fatalf("command metadata = exit %d (present=%v), failed=%v", exitCode, ok, auditMetadata.Failed())
	}
}

func TestEventMetadataQuery_SourceHookFiltersPreserveLegacyFallback(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	primary := newEventForSQLiteTest(t, "event-primary", "hook", "codex", "session-1", "ws", "done", time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC))
	primary.SetSourceHook("stop")
	if err := sut.Save(context.Background(), primary); err != nil {
		t.Fatalf("Save(primary) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, source_hook, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`, "event-legacy", "session_ended", "hook", "codex", "session-1", "ws", "[phase:subagent] done", time.Date(2026, 7, 22, 2, 1, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close DB: %v", err)
	}

	primaryMetadata, err := sut.ListRecentMetadata(context.Background(), apptypes.NewEventListCriteriaBuilder(10).SourceHook("stop").Build())
	if err != nil {
		t.Fatalf("ListRecentMetadata(primary) error = %v", err)
	}
	if diff := cmp.Diff([]string{"event-primary"}, metadataIDs(primaryMetadata)); diff != "" {
		t.Fatalf("primary source-hook mismatch (-want +got):\n%s", diff)
	}

	legacyMetadata, err := sut.ListRecentMetadata(context.Background(), apptypes.NewEventListCriteriaBuilder(10).SourceHook("subagent_stop").Build())
	if err != nil {
		t.Fatalf("ListRecentMetadata(legacy) error = %v", err)
	}
	if diff := cmp.Diff([]string{"event-legacy"}, metadataIDs(legacyMetadata)); diff != "" {
		t.Fatalf("legacy source-hook mismatch (-want +got):\n%s", diff)
	}
}

func TestEventMetadataQuery_SearchAndContextPreserveOrdering(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	for i, body := range []string{"needle one", "needle two"} {
		event := newEventForSQLiteTest(t, "event-"+string(rune('1'+i)), "cli", "codex", "session-1", "duck8823/traceary", body, base.Add(time.Duration(i)*time.Second))
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	searchCriteria := apptypes.NewEventSearchCriteriaBuilder(10).
		Query("needle").
		Workspace(types.Workspace("duck8823/traceary")).
		Build()
	search, err := sut.SearchMetadata(context.Background(), searchCriteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff([]string{"event-2", "event-1"}, metadataIDs(search)); diff != "" {
		t.Fatalf("search order mismatch (-want +got):\n%s", diff)
	}

	contextCriteria := apptypes.NewEventContextCriteriaBuilder(10).
		Workspace(types.Workspace("duck8823/traceary")).
		SessionID(types.SessionID("session-1")).
		Build()
	contextEvents, err := sut.GetContextMetadata(context.Background(), contextCriteria)
	if err != nil {
		t.Fatalf("GetContextMetadata() error = %v", err)
	}
	if diff := cmp.Diff([]string{"event-2", "event-1"}, metadataIDs(contextEvents)); diff != "" {
		t.Fatalf("context order mismatch (-want +got):\n%s", diff)
	}
}

func TestEventMetadataQuery_ListWindowPreservesSnapshotPagingSemantics(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	const total = 205
	for i := range total {
		event := newEventForSQLiteTest(
			t,
			fmt.Sprintf("event-%03d", i),
			"cli",
			"codex",
			"session-1",
			"duck8823/traceary",
			strings.Repeat("body", i%11+1),
			base.Add(time.Duration(i)*time.Second),
		)
		if err := sut.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(event-%03d) error = %v", i, err)
		}
	}

	var pageSizes []int
	sut.SetListWindowBatchHookForTest(func(_ int, batchSize int) {
		pageSizes = append(pageSizes, batchSize)
	})
	criteria := apptypes.NewEventListCriteriaBuilder(100).
		From(base).
		To(base.Add(total * time.Second)).
		Build()
	got, err := sut.ListWindowMetadata(context.Background(), criteria)
	if err != nil {
		t.Fatalf("ListWindowMetadata() error = %v", err)
	}
	if len(got) != total {
		t.Fatalf("len(metadata) = %d, want %d", len(got), total)
	}
	if diff := cmp.Diff([]int{100, 100, 5}, pageSizes); diff != "" {
		t.Fatalf("page sizes mismatch (-want +got):\n%s", diff)
	}
	if got[0].EventID().String() != "event-204" || got[len(got)-1].EventID().String() != "event-000" {
		t.Fatalf("metadata order = %s ... %s, want event-204 ... event-000", got[0].EventID(), got[len(got)-1].EventID())
	}
}

func TestEventBodyMetadataMigration_BackfillsAndTracksStoredBytes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	initialMigrations := fstest.MapFS{
		"000001_init.sql": {Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    source_hook TEXT,
    created_at TEXT NOT NULL
);`)},
	}
	_, initialStoreManager := newEventDatasource(t, dbPath, initialMigrations)
	if err := initialStoreManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(initial) error = %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(initial) error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "event-historical", "note", "cli", "codex", "session-1", "ws", "日本語", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert historical event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close initial DB: %v", err)
	}

	migrationPath := filepath.Join(onDiskSQLiteMigrationDir(t), "000021_add_event_body_metadata.sql")
	migrationSQL, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	updatedMigrations := fstest.MapFS{
		"000001_init.sql":                    initialMigrations["000001_init.sql"],
		"000021_add_event_body_metadata.sql": {Data: migrationSQL},
	}
	sut, storeManager := newEventDatasource(t, dbPath, updatedMigrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(updated) error = %v", err)
	}

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(updated) error = %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close updated DB: %v", err)
		}
	}()

	var historicalBytes int
	var historicalOriginal, historicalIngest, historicalStorage sql.NullInt64
	if err := db.QueryRow(`SELECT body_stored_bytes, body_original_bytes, body_ingest_truncated, body_storage_truncated FROM events WHERE id = ?`, "event-historical").Scan(&historicalBytes, &historicalOriginal, &historicalIngest, &historicalStorage); err != nil {
		t.Fatalf("query historical stored bytes: %v", err)
	}
	if historicalBytes != len("日本語") {
		t.Fatalf("historical body_stored_bytes = %d, want %d", historicalBytes, len("日本語"))
	}
	if historicalOriginal.Valid || historicalIngest.Valid || historicalStorage.Valid {
		t.Fatalf("historical unknown facts became known: original=%v ingest=%v storage=%v", historicalOriginal, historicalIngest, historicalStorage)
	}

	event := newEventForSQLiteTest(t, "event-utf8", "cli", "codex", "session-1", "ws", "日本語", time.Now().UTC().Add(time.Second))
	if err := sut.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var storedBytes int
	if err := db.QueryRow(`SELECT body_stored_bytes FROM events WHERE id = ?`, "event-utf8").Scan(&storedBytes); err != nil {
		t.Fatalf("query stored bytes: %v", err)
	}
	if storedBytes != len("日本語") {
		t.Fatalf("body_stored_bytes = %d, want %d", storedBytes, len("日本語"))
	}
	if _, err := db.Exec(`UPDATE events SET body = ? WHERE id = ?`, "updated", "event-utf8"); err != nil {
		t.Fatalf("update body: %v", err)
	}
	if err := db.QueryRow(`SELECT body_stored_bytes FROM events WHERE id = ?`, "event-utf8").Scan(&storedBytes); err != nil {
		t.Fatalf("query updated stored bytes: %v", err)
	}
	if storedBytes != len("updated") {
		t.Fatalf("updated body_stored_bytes = %d, want %d", storedBytes, len("updated"))
	}
}

func metadataIDs(metadata []apptypes.EventMetadata) []string {
	ids := make([]string, 0, len(metadata))
	for _, event := range metadata {
		ids = append(ids, event.EventID().String())
	}
	return ids
}
