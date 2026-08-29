package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func newReadOnlyAuditFixture(t *testing.T, id, output string, metadata types.ReadOnlyOutputMetadata) (*model.Event, *model.CommandAudit) {
	t.Helper()
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("claude")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	event := model.EventOf(
		eventID,
		types.EventKindCommandExecuted,
		"hook",
		agent,
		sessionID,
		"duck8823/traceary",
		"",
		time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	)
	audit, err := model.NewCommandAudit(eventID, "Read", `{"file_path":"README.md"}`, output, false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	if err := audit.ClassifyOutcome(types.None[int](), types.CommandFailureReasonNone, false); err != nil {
		t.Fatalf("ClassifyOutcome() error = %v", err)
	}
	audit.SetReadOnlyOutputMetadata(metadata)
	return event, audit
}

func TestEventDatasource_SaveWithAudit_PersistsOutputMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	metadata := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
	event, audit := newReadOnlyAuditFixture(t, "event-meta-1", "", metadata)
	if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var (
		outputText      string
		outputTruncated int
		outputOriginal  int
		outputMetadata  sql.NullString
	)
	if err := db.QueryRow(`
SELECT output_text, output_truncated, output_original_bytes, output_metadata
  FROM command_audits WHERE event_id = ?`, "event-meta-1").Scan(
		&outputText, &outputTruncated, &outputOriginal, &outputMetadata,
	); err != nil {
		t.Fatalf("audit query error = %v", err)
	}
	if outputTruncated != 0 {
		t.Fatalf("output_truncated = %d, want 0", outputTruncated)
	}
	if outputOriginal != 0 {
		t.Fatalf("output_original_bytes = %d, want 0", outputOriginal)
	}
	if !outputMetadata.Valid || outputMetadata.String == "" {
		t.Fatal("output_metadata is NULL, want canonical JSON")
	}
	encoded, err := types.EncodeReadOnlyOutputMetadata(metadata)
	if err != nil {
		t.Fatalf("EncodeReadOnlyOutputMetadata() error = %v", err)
	}
	if diff := cmp.Diff(encoded, outputMetadata.String); diff != "" {
		t.Fatalf("output_metadata mismatch (-want +got):\n%s", diff)
	}
}

func TestEventDatasource_SaveWithAudit_SkipsOutputMetadataOnLegacySchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 77))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	metadata := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
	event, audit := newReadOnlyAuditFixture(t, "event-legacy-1", "", metadata)
	if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('command_audits') WHERE name = 'output_metadata'`).Scan(&count); err != nil {
		t.Fatalf("pragma query error = %v", err)
	}
	if count != 0 {
		t.Fatalf("legacy schema unexpectedly has output_metadata")
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM command_audits WHERE event_id = ?`, "event-legacy-1").Scan(&rows); err != nil {
		t.Fatalf("count query error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("command_audits rows = %d, want 1", rows)
	}
}

func TestEventDatasource_GetEventDetails_RestoresOutputMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	metadata := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
	event, audit := newReadOnlyAuditFixture(t, "event-details-1", "", metadata)
	if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	got, err := sut.GetDetails(context.Background(), types.EventID("event-details-1"))
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	restored, ok := got.CommandAudit().Value()
	if !ok {
		t.Fatal("CommandAudit() is empty")
	}
	if diff := cmp.Diff("", restored.Output()); diff != "" {
		t.Fatalf("Output() mismatch (-want +got):\n%s", diff)
	}
	gotMetadata, ok := restored.OutputMetadata().Value()
	if !ok {
		t.Fatal("OutputMetadata() is None after restore")
	}
	if diff := cmp.Diff(metadata.SHA256(), gotMetadata.SHA256()); diff != "" {
		t.Fatalf("SHA256 mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(metadata.Paths(), gotMetadata.Paths()); diff != "" {
		t.Fatalf("Paths mismatch (-want +got):\n%s", diff)
	}
}

func TestBundleDatasource_CarriesOutputMetadata(t *testing.T) {
	t.Parallel()

	srcPath := filepath.Join(t.TempDir(), "src", "traceary.db")
	srcEvents, srcStore := newEventDatasource(t, srcPath, onDiskSQLiteMigrations(t))
	if err := srcStore.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(src) error = %v", err)
	}
	srcDB := sqlite.NewDatabase(srcPath, onDiskSQLiteMigrations(t))
	srcBundle := sqlite.NewBundleDatasource(srcDB, srcEvents)

	metadata := types.ReadOnlyOutputMetadataOf([]string{"README.md"}, "hello", 64)
	event, audit := newReadOnlyAuditFixture(t, "event-bundle-1", "", metadata)
	if err := srcEvents.SaveWithAudit(context.Background(), event, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}
	exported, err := srcBundle.ListBundleCommandAudits(context.Background())
	if err != nil {
		t.Fatalf("ListBundleCommandAudits() error = %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("exported audits = %d, want 1", len(exported))
	}

	dstPath := filepath.Join(t.TempDir(), "dst", "traceary.db")
	dstEvents, dstStore := newEventDatasource(t, dstPath, onDiskSQLiteMigrations(t))
	if err := dstStore.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(dst) error = %v", err)
	}
	dstDB := sqlite.NewDatabase(dstPath, onDiskSQLiteMigrations(t))
	dstBundle := sqlite.NewBundleDatasource(dstDB, dstEvents)
	tx, err := dstBundle.BeginBundleImport(context.Background())
	if err != nil {
		t.Fatalf("BeginBundleImport() error = %v", err)
	}
	if _, err := tx.ImportEvent(context.Background(), event, usecase.BundleConflictError); err != nil {
		t.Fatalf("ImportEvent() error = %v", err)
	}
	if _, err := tx.ImportCommandAudit(context.Background(), exported[0], usecase.BundleConflictError); err != nil {
		t.Fatalf("ImportCommandAudit() error = %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	got, err := dstEvents.GetDetails(context.Background(), types.EventID("event-bundle-1"))
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	restored, ok := got.CommandAudit().Value()
	if !ok {
		t.Fatal("imported CommandAudit() is empty")
	}
	gotMetadata, ok := restored.OutputMetadata().Value()
	if !ok {
		t.Fatal("imported OutputMetadata() is None")
	}
	if diff := cmp.Diff(metadata.SHA256(), gotMetadata.SHA256()); diff != "" {
		t.Fatalf("imported SHA256 mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("", restored.Output()); diff != "" {
		t.Fatalf("imported Output mismatch (-want +got):\n%s", diff)
	}
}
