//go:build unix

package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestFileRetentionBackupPlanApplyAndRetry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	root := t.TempDir()
	livePath := filepath.Join(t.TempDir(), "live.db")
	createFileRetentionSQLite(t, livePath)
	copyFileRetentionTestFile(t, livePath, filepath.Join(root, "old.db"))
	copyFileRetentionTestFile(t, livePath, filepath.Join(root, "new.db"))
	if err := os.Chtimes(filepath.Join(root, "old.db"), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Chtimes(old) error = %v", err)
	}
	if err := os.Chtimes(filepath.Join(root, "new.db"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes(new) error = %v", err)
	}
	writeFileRetentionBackupManifest(t, livePath, filepath.Join(root, "old.db"), now.Add(-2*time.Hour))
	writeFileRetentionBackupManifest(t, livePath, filepath.Join(root, "new.db"), now.Add(-time.Hour))

	datasource := NewFileRetentionDatasource()
	snapshot, err := datasource.InspectFileRetention(context.Background(), apptypes.FileRetentionInventoryRequest{Class: "backup", Root: root, DatabasePath: livePath})
	if err != nil {
		t.Fatalf("InspectFileRetention() error = %v", err)
	}
	if len(snapshot.Entries) != 2 || !snapshot.Entries[0].Verified || !snapshot.Entries[1].Verified {
		t.Fatalf("snapshot entries = %#v, want two verified backups", snapshot.Entries)
	}

	workflow := usecase.NewFileRetentionUsecase(datasource, datasource)
	maxCount := 1
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: livePath, ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "backup", Root: root, Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	var plan apptypes.FileRetentionPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("Unmarshal(plan) error = %v", err)
	}
	classPlan := plan.CanonicalPayload.Classes[0]
	if len(classPlan.Candidates) != 1 || classPlan.Candidates[0].RelativePath != "old.db" || classPlan.Floor == nil || classPlan.Floor.RelativePath != "new.db" {
		t.Fatalf("class plan = %#v, want old candidate and new floor", classPlan)
	}

	injected := errors.New("injected crash")
	datasource.afterBoundary = func(boundary string) error {
		if boundary == "original-unlinked" {
			datasource.afterBoundary = nil
			return injected
		}
		return nil
	}
	if _, err := workflow.Apply(context.Background(), planBytes, plan.PlanID, now.Add(time.Minute)); !errors.Is(err, injected) {
		t.Fatalf("Apply(crash) error = %v, want injected", err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.db")); !os.IsNotExist(err) {
		t.Fatalf("old backup after crash stat error = %v, want absent original", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.db")); err != nil {
		t.Fatalf("new floor after crash error = %v", err)
	}

	result, err := workflow.Apply(context.Background(), planBytes, plan.PlanID, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Apply(retry) error = %v", err)
	}
	if result.DeletedCount != 1 || result.ConflictedCount != 0 {
		t.Fatalf("Apply(retry) result = %#v", result)
	}
	ledgerData, err := os.ReadFile(filepath.Join(root, fileRetentionLedgerName))
	if err != nil {
		t.Fatalf("ReadFile(ledger) error = %v", err)
	}
	var ledger fileRetentionLedger
	if err := json.Unmarshal(ledgerData, &ledger); err != nil {
		t.Fatalf("Unmarshal(ledger) error = %v", err)
	}
	if len(ledger.Entries) != 1 || ledger.Entries[0].Identity != classPlan.Candidates[0].Identity {
		t.Fatalf("ledger = %#v, want one exact candidate", ledger)
	}

	result, err = workflow.Apply(context.Background(), planBytes, plan.PlanID, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("Apply(idempotent) error = %v", err)
	}
	if result.AlreadyCommitted != 1 || result.DeletedCount != 0 {
		t.Fatalf("Apply(idempotent) result = %#v", result)
	}
}

func TestFileRetentionInventoryFailsClosedForSymlinkAndHardLink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	livePath := filepath.Join(t.TempDir(), "live.db")
	createFileRetentionSQLite(t, livePath)
	backup := filepath.Join(root, "backup.db")
	copyFileRetentionTestFile(t, livePath, backup)
	if err := os.Link(backup, filepath.Join(root, "backup-hardlink.db")); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if err := os.Symlink(livePath, filepath.Join(root, "escape.db")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	datasource := NewFileRetentionDatasource()
	snapshot, err := datasource.InspectFileRetention(context.Background(), apptypes.FileRetentionInventoryRequest{Class: "backup", Root: root, DatabasePath: livePath})
	if err != nil {
		t.Fatalf("InspectFileRetention() error = %v", err)
	}
	if len(snapshot.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 blockers", len(snapshot.Entries))
	}
	reasons := make(map[string]bool)
	for _, entry := range snapshot.Entries {
		reasons[entry.BlockingReason] = true
	}
	if !reasons["hard_link"] || !reasons["non_regular"] {
		t.Fatalf("blocking reasons = %v, want hard_link and non_regular", reasons)
	}

	maxCount := 1
	workflow := usecase.NewFileRetentionUsecase(datasource, datasource)
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: livePath, ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "backup", Root: root, Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	if !strings.Contains(string(planBytes), `"status": "indeterminate"`) || strings.Contains(string(planBytes), `"candidates": [\n        {`) {
		t.Fatalf("plan does not fail closed: %s", planBytes)
	}
}

func TestFileRetentionArchivePlanUsesManifestGenerationAndCreatedAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	root := t.TempDir()
	livePath := filepath.Join(t.TempDir(), "live.db")
	createFileRetentionSQLite(t, livePath)
	writeFileRetentionArchiveFixture(t, filepath.Join(root, "old.trcaryar"), livePath, now.Add(-2*time.Hour))
	writeFileRetentionArchiveFixture(t, filepath.Join(root, "new.trcaryar"), livePath, now.Add(-time.Hour))

	datasource := NewFileRetentionDatasource()
	workflow := usecase.NewFileRetentionUsecase(datasource, datasource)
	maxCount := 1
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: livePath, ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "archive", Root: root, Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	var plan apptypes.FileRetentionPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("Unmarshal(plan) error = %v", err)
	}
	classPlan := plan.CanonicalPayload.Classes[0]
	if len(classPlan.Candidates) != 1 || classPlan.Candidates[0].RelativePath != "old.trcaryar" || classPlan.Floor == nil || classPlan.Floor.RelativePath != "new.trcaryar" {
		t.Fatalf("archive class plan = %#v", classPlan)
	}
}

func TestFileRetentionCrashBoundariesConverge(t *testing.T) {
	boundaries := []string{
		"lease-acquired", "pending", "catalog-deleting", "journal-running", "journal-tombstone_intent",
		"tombstone-linked", "journal-tombstoned", "journal-unlink_original_intent", "original-unlinked",
		"journal-original_unlinked", "journal-unlink_tombstone_intent", "tombstone-unlinked",
		"journal-tombstone_unlinked", "journal-metadata_unlink_intent", "metadata-unlinked", "journal-metadata_unlinked",
		"journal-catalog_commit_intent", "ledger-recorded",
		"journal-ledger_recorded", "catalog-deleted", "journal-catalog_deleted", "journal-committed", "lease-released",
	}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
			root, planBytes, plan, datasource, workflow := newFileRetentionBackupPlanFixture(t, now)
			injected := errors.New("injected crash")
			datasource.afterBoundary = func(got string) error {
				if got == boundary {
					datasource.afterBoundary = nil
					return injected
				}
				return nil
			}
			if _, err := workflow.Apply(context.Background(), planBytes, plan.PlanID, now); !errors.Is(err, injected) {
				t.Fatalf("Apply(%s) error = %v, want injected", boundary, err)
			}
			result, err := workflow.Apply(context.Background(), planBytes, plan.PlanID, now.Add(2*time.Minute))
			if err != nil {
				t.Fatalf("Apply(%s retry) error = %v", boundary, err)
			}
			if result.ConflictedCount != 0 || result.DeletedCount+result.AlreadyCommitted != 1 {
				t.Fatalf("Apply(%s retry) result = %#v", boundary, result)
			}
			if _, err := os.Stat(filepath.Join(root, "old.db")); !os.IsNotExist(err) {
				t.Fatalf("old.db after %s retry stat error = %v", boundary, err)
			}
			if _, err := os.Stat(filepath.Join(root, "new.db")); err != nil {
				t.Fatalf("new.db after %s retry error = %v", boundary, err)
			}
			oldManifest := filepath.Join(root, apptypes.BackupRetentionManifestName("old.db"))
			if _, err := os.Stat(oldManifest); !os.IsNotExist(err) {
				t.Fatalf("old manifest after %s retry stat error = %v", boundary, err)
			}
			if _, err := os.Stat(filepath.Join(root, apptypes.BackupRetentionManifestName("new.db"))); err != nil {
				t.Fatalf("new manifest after %s retry error = %v", boundary, err)
			}
			ledgerData, err := os.ReadFile(filepath.Join(root, fileRetentionLedgerName))
			if err != nil {
				t.Fatalf("ReadFile(%s ledger) error = %v", boundary, err)
			}
			var ledger fileRetentionLedger
			if err := json.Unmarshal(ledgerData, &ledger); err != nil || len(ledger.Entries) != 1 {
				t.Fatalf("%s ledger = %#v, error = %v", boundary, ledger, err)
			}
			catalogData, err := os.ReadFile(filepath.Join(root, fileRetentionCatalogName))
			if err != nil {
				t.Fatalf("ReadFile(%s catalog) error = %v", boundary, err)
			}
			var catalog fileRetentionCatalog
			if err := json.Unmarshal(catalogData, &catalog); err != nil {
				t.Fatalf("Unmarshal(%s catalog) error = %v", boundary, err)
			}
			candidate := plan.CanonicalPayload.Classes[0].Candidates[0]
			if catalog.Lease != nil || catalog.Items[candidate.Identity].State != "deleted" {
				t.Fatalf("%s catalog = %#v", boundary, catalog)
			}
			journalName := fileRetentionJournalName(plan.PlanID, candidate.Identity)
			journalData, err := os.ReadFile(filepath.Join(root, journalName))
			if err != nil {
				t.Fatalf("ReadFile(%s journal) error = %v", boundary, err)
			}
			var journal fileRetentionJournal
			if err := json.Unmarshal(journalData, &journal); err != nil || journal.State != "committed" {
				t.Fatalf("%s journal = %#v, error = %v", boundary, journal, err)
			}
		})
	}
}

func createFileRetentionSQLite(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE evidence (id INTEGER PRIMARY KEY, value TEXT NOT NULL); INSERT INTO evidence(value) VALUES ('ok')`); err != nil {
		_ = database.Close()
		t.Fatalf("create SQLite fixture error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close(SQLite) error = %v", err)
	}
}

func newFileRetentionBackupPlanFixture(
	t *testing.T,
	now time.Time,
) (string, []byte, apptypes.FileRetentionPlan, *FileRetentionDatasource, usecase.FileRetentionUsecase) {
	t.Helper()
	root := t.TempDir()
	livePath := filepath.Join(t.TempDir(), "live.db")
	createFileRetentionSQLite(t, livePath)
	for index, name := range []string{"old.db", "new.db"} {
		backup := filepath.Join(root, name)
		copyFileRetentionTestFile(t, livePath, backup)
		createdAt := now.Add(time.Duration(index-2) * time.Hour)
		if err := os.Chtimes(backup, createdAt, createdAt); err != nil {
			t.Fatalf("Chtimes(%s) error = %v", name, err)
		}
		writeFileRetentionBackupManifest(t, livePath, backup, createdAt)
	}
	datasource := NewFileRetentionDatasource()
	workflow := usecase.NewFileRetentionUsecase(datasource, datasource)
	maxCount := 1
	planBytes, err := workflow.CreatePlan(context.Background(), apptypes.FileRetentionPlanRequest{
		DatabasePath: livePath, ExpiresAfter: time.Hour,
		Classes: []apptypes.FileRetentionClassRequest{{Class: "backup", Root: root, Budget: apptypes.FileRetentionBudgetInput{MaxCount: &maxCount}}},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	var plan apptypes.FileRetentionPlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatalf("Unmarshal(plan) error = %v", err)
	}
	return root, planBytes, plan, datasource, workflow
}

func copyFileRetentionTestFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", source, err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", destination, err)
	}
}

func writeFileRetentionBackupManifest(t *testing.T, source, backup string, createdAt time.Time) {
	t.Helper()
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile(backup) error = %v", err)
	}
	digest := sha256.Sum256(data)
	lineage, err := domtypes.FileRetentionLineageFromPath("backup", source)
	if err != nil {
		t.Fatalf("FileRetentionLineageFromPath() error = %v", err)
	}
	manifest := apptypes.BackupRetentionManifest{
		SchemaVersion: apptypes.BackupRetentionManifestSchema,
		RelativePath:  filepath.Base(backup),
		BackupSHA256:  hex.EncodeToString(digest[:]),
		SourceLineage: lineage,
		CreatedAt:     createdAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(backup manifest) error = %v", err)
	}
	manifestPath := filepath.Join(filepath.Dir(backup), apptypes.BackupRetentionManifestName(filepath.Base(backup)))
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(backup manifest) error = %v", err)
	}
}

func writeFileRetentionArchiveFixture(t *testing.T, destination, sourceDB string, createdAt time.Time) {
	t.Helper()
	tableData := []byte("{\"id\":\"event-1\"}\n")
	tableDigest := sha256HexBytes(tableData)
	payloadDigest := sha256HexString("events:" + tableDigest + "\n")
	manifest := map[string]any{
		"schema_version":        1,
		"format":                "traceary.store.archive",
		"created_at":            createdAt.UTC().Format(time.RFC3339Nano),
		"source_db_fingerprint": map[string]any{"path": sourceDB},
		"tables":                []map[string]any{{"name": "events", "ndjson_sha256": tableDigest}},
		"payload_sha256":        payloadDigest,
		"encryption":            map[string]any{"mode": "none"},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(archive manifest) error = %v", err)
	}
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, data := range map[string][]byte{"tables/events.ndjson": tableData, "manifest.json": manifestData} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatalf("WriteHeader(%s) error = %v", name, err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			t.Fatalf("Write(%s) error = %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("Close(tar) error = %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("Close(gzip) error = %v", err)
	}
	payload := append([]byte("TRCARYAR\x01"), compressed.Bytes()...)
	if err := os.WriteFile(destination, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(archive) error = %v", err)
	}
}
