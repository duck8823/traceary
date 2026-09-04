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
	audit, err := model.NewCommandAudit("bundle-zstd", "go test "+plain, plain, plain, false, false)
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

	conn := openCallSiteDB(t, dbPath)
	assertCallSiteBodyCodec(t, conn, "bundle-zstd", "zstd")
	assertCallSiteAuditCodecs(t, conn, "bundle-zstd", "zstd")
	details, err := events.GetDetails(ctx, event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().Body() != plain {
		t.Fatalf("imported event body mismatch: got %d bytes", len(details.Event().Body()))
	}
	gotAudit, ok := details.CommandAudit().Value()
	if !ok {
		t.Fatal("imported audit missing")
	}
	if gotAudit.Command() != audit.Command() || gotAudit.Input() != plain || gotAudit.Output() != plain {
		t.Fatalf("imported audit payload mismatch")
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
	if _, err := conn.Exec(`INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, exit_code, failed)
VALUES ('archive-zstd', ?, ?, ?, 0, 0, 0, 0)`, "go test "+plain, plain, plain); err != nil {
		t.Fatalf("insert audit: %v", err)
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
	assertCallSiteAuditCodecs(t, conn, "archive-zstd", "zstd")
	events := sqlite.NewEventDatasource(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t)))
	details, err := events.GetDetails(ctx, types.EventID("archive-zstd"))
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().Body() != plain {
		t.Fatalf("restored body mismatch: got %d bytes", len(details.Event().Body()))
	}
	gotAudit, ok := details.CommandAudit().Value()
	if !ok {
		t.Fatal("restored audit missing")
	}
	if gotAudit.Input() != plain || gotAudit.Output() != plain || gotAudit.Command() != strings.TrimSpace("go test "+plain) {
		t.Fatalf("restored audit payload mismatch")
	}
}

func TestRawBodyRestore_UsesCanonicalPayloadCodec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	plain := compressiblePayloadBody()
	event := rawBodyRetentionEvent(t, "retention-zstd", plain, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(ctx, event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())

	snapshot, err := store.ListRawBodyCandidates(ctx, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	if len(snapshot.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(snapshot.Candidates))
	}
	planID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := store.ApplyRawBodyPlan(ctx, snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("ApplyRawBodyPlan() error = %v", err)
	}

	recovery := []apptypes.RawBodyRecoveryBody{{Candidate: snapshot.Candidates[0], Body: plain}}
	restored, err := store.RestoreRawBodyPlan(ctx, snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, recovery, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RestoreRawBodyPlan() error = %v", err)
	}
	if restored.RestoredCount != 1 {
		t.Fatalf("restore result = %+v", restored)
	}

	assertCallSiteBodyCodec(t, openCallSiteDB(t, dbPath), "retention-zstd", "zstd")
	details, err := events.GetDetails(ctx, event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().Body() != plain {
		t.Fatalf("restored raw-body mismatch: got %d bytes", len(details.Event().Body()))
	}
}

func assertCallSiteAuditCodecs(t *testing.T, db *sql.DB, id, want string) {
	t.Helper()
	var commandCodec, inputCodec, outputCodec sql.NullString
	if err := db.QueryRow(
		`SELECT command_codec, input_codec, output_codec FROM command_audits WHERE event_id = ?`,
		id,
	).Scan(&commandCodec, &inputCodec, &outputCodec); err != nil {
		t.Fatalf("read audit codecs %s: %v", id, err)
	}
	for field, got := range map[string]sql.NullString{
		"command_codec": commandCodec,
		"input_codec":   inputCodec,
		"output_codec":  outputCodec,
	} {
		if !got.Valid {
			t.Fatalf("%s for %s is NULL, want %q", field, id, want)
		}
		if got.String != want {
			t.Fatalf("%s for %s = %q, want %q", field, id, got.String, want)
		}
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
