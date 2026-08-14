package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func compressiblePayloadBody() string {
	return strings.Repeat("redacted synthetic payload ", 256)
}

func TestBundleImport_UsesCanonicalPayloadCodec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	events := sqlite.NewEventDatasource(database)
	bundles := sqlite.NewBundleDatasource(database, events)

	plain := compressiblePayloadBody()
	event := model.EventOf(
		types.EventID("bundle-zstd"),
		types.EventKindPrompt,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID("bundle-session"),
		types.Workspace("ws"),
		plain,
		time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	)
	audit, err := model.NewCommandAudit("bundle-zstd", "go test ./...", "", plain, false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	tx, err := bundles.BeginBundleImport(ctx)
	if err != nil {
		t.Fatalf("BeginBundleImport() error = %v", err)
	}
	if imported, err := tx.ImportEvent(ctx, event, usecase.BundleConflictError); err != nil || !imported {
		_ = tx.Rollback(ctx)
		t.Fatalf("ImportEvent() = %v/%v", imported, err)
	}
	if imported, err := tx.ImportCommandAudit(ctx, audit, usecase.BundleConflictError); err != nil || !imported {
		_ = tx.Rollback(ctx)
		t.Fatalf("ImportCommandAudit() = %v/%v", imported, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	assertCallSiteBodyCodec(t, openCallSiteDB(t, dbPath), "bundle-zstd", "zstd")
	details, err := events.GetDetails(ctx, event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().Body() != plain {
		t.Fatalf("imported event body mismatch: got %d bytes", len(details.Event().Body()))
	}
	audit, ok := details.CommandAudit().Value()
	if !ok || audit.Output() != plain {
		t.Fatalf("imported audit output mismatch")
	}
}

func TestArchiveRestore_UsesCanonicalPayloadCodec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "traceary.db")
	store := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	plain := compressiblePayloadBody()
	conn := openCallSiteDB(t, dbPath)
	if _, err := conn.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
VALUES ('archive-zstd', 'note', 'cli', 'manual', 's1', '', ?, '2020-01-01T00:00:00Z', '')`, plain); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	uc := usecase.NewStoreManagementUsecase(store)
	archivePath := filepath.Join(dir, "out.trcaryar")
	if _, err := uc.CreateStoreArchive(ctx, apptypes.StoreArchiveCreateParams{
		OutputPath:        archivePath,
		Before:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		KeepDays:          90,
		Target:            apptypes.GarbageCollectionTargetEvents,
		DeleteAfterVerify: true,
		ToolVersion:       "test",
	}); err != nil {
		t.Fatalf("CreateStoreArchive() error = %v", err)
	}

	if _, err := uc.RestoreStoreArchive(ctx, archivePath, nil, false); err != nil {
		t.Fatalf("RestoreStoreArchive() error = %v", err)
	}
	assertCallSiteBodyCodec(t, conn, "archive-zstd", "zstd")
	events := sqlite.NewEventDatasource(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t)))
	details, err := events.GetDetails(ctx, types.EventID("archive-zstd"))
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().Body() != plain {
		t.Fatalf("restored body mismatch: got %d bytes", len(details.Event().Body()))
	}
}

func TestDedupeRestore_UsesCanonicalPayloadCodec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	plain := compressiblePayloadBody()
	conn := openCallSiteDB(t, dbPath)
	for _, id := range []string{"dedupe-a1", "dedupe-a2"} {
		if _, err := conn.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, 'prompt', 'codex', 's1', 'w1', ?, ?, 'user_prompt_submit', 'hook')`,
			id, plain, map[string]string{"dedupe-a1": "2026-04-10T00:00:00Z", "dedupe-a2": "2026-04-10T00:00:03Z"}[id],
		); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	if _, err := store.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-codec", Now: now,
	}); err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}
	if _, err := store.RestoreContentEventDedupeRun(ctx, "dedupe-codec"); err != nil {
		t.Fatalf("RestoreContentEventDedupeRun() error = %v", err)
	}

	assertCallSiteBodyCodec(t, conn, "dedupe-a2", "zstd")
	details, err := events.GetDetails(ctx, types.EventID("dedupe-a2"))
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().Body() != plain {
		t.Fatalf("restored dedupe body mismatch: got %d bytes", len(details.Event().Body()))
	}
}

func assertCallSiteBodyCodec(t *testing.T, db *sql.DB, id, want string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRow(`SELECT body_codec FROM events WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("read body_codec %s: %v", id, err)
	}
	if !got.Valid {
		t.Fatalf("body_codec for %s is NULL, want %q", id, want)
	}
	if got.String != want {
		t.Fatalf("body_codec for %s = %q, want %q", id, got.String, want)
	}
}

func openCallSiteDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
