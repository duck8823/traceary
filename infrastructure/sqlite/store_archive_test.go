package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

func TestStoreArchive_createDeleteRestore_events(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "traceary.db")
	storeManager := newStoreManagementDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	conn, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
VALUES ('old-e1', 'note', 'cli', 'manual', 's1', '', 'body', '2020-01-01T00:00:00Z', '')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook)
VALUES ('new-e1', 'note', 'cli', 'manual', 's1', '', 'body', '2099-01-01T00:00:00Z', '')`)
	if err != nil {
		t.Fatal(err)
	}

	sut := usecase.NewStoreManagementUsecase(storeManager)
	archivePath := filepath.Join(dir, "out.trcaryar")
	result, err := sut.CreateStoreArchive(context.Background(), apptypes.StoreArchiveCreateParams{
		OutputPath:        archivePath,
		Before:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		KeepDays:          90,
		Target:            apptypes.GarbageCollectionTargetEvents,
		DeleteAfterVerify: true,
		ToolVersion:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalRows < 1 {
		t.Fatalf("expected at least 1 archived row, got %+v", result)
	}
	if !result.DeletedAfterVerify {
		t.Fatalf("expected delete after verify: %+v", result)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM events WHERE id = 'old-e1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("old event still present")
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM events WHERE id = 'new-e1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("new event missing")
	}

	restored, err := sut.RestoreStoreArchive(context.Background(), archivePath, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inserted < 1 {
		t.Fatalf("restore inserted=%d", restored.Inserted)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM events WHERE id = 'old-e1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("old event not restored")
	}

	again, err := sut.RestoreStoreArchive(context.Background(), archivePath, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Skipped < 1 {
		t.Fatalf("second restore skipped=%d want >=1", again.Skipped)
	}
}

