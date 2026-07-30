package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

const (
	v0330ReleaseEvidenceEvents          = 130
	v0330ReleaseEvidenceLargeEvents     = 8
	v0330ReleaseEvidenceBodyBytes       = 256 << 20
	v0330ReleaseEvidenceMinimumDBBytes  = int64(2 << 30)
	v0330ReleaseEvidenceMeasurementRuns = 25
	v0330ReleaseEvidenceProbeLimit      = 20
)

type v0330ReleaseEvidencePhaseB struct {
	SourceManagedBytes          int64   `json:"source_managed_bytes"`
	ScratchBytesAfterCheckpoint int64   `json:"scratch_bytes_after_checkpoint"`
	Events                      int64   `json:"events"`
	MigrationMS                 float64 `json:"migration_ms"`
	ResumeBackfillMS            float64 `json:"resume_backfill_ms"`
	Migrations31Through34       bool    `json:"migrations_31_34_applied"`
	ProjectionRows              int64   `json:"projection_rows"`
	IntegrityOK                 bool    `json:"integrity_ok"`
	ForeignKeyViolations        int64   `json:"foreign_key_violations"`
	SourceUnchanged             bool    `json:"source_unchanged"`
	InitialFTSDocuments         int64   `json:"initial_fts_documents"`
	InitialFTSComplete          bool    `json:"initial_fts_complete"`
	FinalFTSDocuments           int64   `json:"final_fts_documents"`
	FinalFTSComplete            bool    `json:"final_fts_complete"`
	PreProjectionWriterOK       bool    `json:"pre_projection_writer_ok"`
}

type v0330ReleaseEvidenceProbe struct {
	Operation          string  `json:"operation"`
	Projection         string  `json:"projection"`
	FTSPhase           string  `json:"fts_phase"`
	Runs               int     `json:"runs"`
	P95MS              float64 `json:"p95_ms"`
	ReturnedItems      int     `json:"returned_items"`
	ReturnedBodyBytes  int     `json:"returned_body_bytes"`
	SourceBodyBytes    int64   `json:"source_body_bytes,omitempty"`
	BoundedBudgetBytes int     `json:"bounded_budget_bytes,omitempty"`
}

type v0330ReleaseEvidencePhaseBC struct {
	PhaseB v0330ReleaseEvidencePhaseB  `json:"phase_b"`
	PhaseC []v0330ReleaseEvidenceProbe `json:"phase_c"`
}

type v0330ReleaseEvidenceSample struct {
	items     int
	bodyBytes int
	identity  [sha256.Size]byte
}

func TestValidateV0330ReleaseEvidenceProjectionPair(t *testing.T) {
	t.Parallel()

	identity := sha256.Sum256([]byte("synthetic fixture membership"))
	validMetadata := v0330ReleaseEvidenceSample{
		items:    v0330ReleaseEvidenceProbeLimit,
		identity: identity,
	}
	validBounded := v0330ReleaseEvidenceSample{
		items:     v0330ReleaseEvidenceProbeLimit,
		bodyBytes: 512,
		identity:  identity,
	}
	if err := validateV0330ReleaseEvidenceProjectionPair(validMetadata, validBounded); err != nil {
		t.Fatalf("valid projection pair rejected: %v", err)
	}

	tests := []struct {
		name     string
		metadata v0330ReleaseEvidenceSample
		bounded  v0330ReleaseEvidenceSample
	}{
		{
			name:     "zero metadata items",
			metadata: v0330ReleaseEvidenceSample{identity: identity},
			bounded:  validBounded,
		},
		{
			name:     "zero bounded body bytes",
			metadata: validMetadata,
			bounded: v0330ReleaseEvidenceSample{
				items:    v0330ReleaseEvidenceProbeLimit,
				identity: identity,
			},
		},
		{
			name:     "different membership",
			metadata: validMetadata,
			bounded: v0330ReleaseEvidenceSample{
				items:     v0330ReleaseEvidenceProbeLimit,
				bodyBytes: 512,
				identity:  sha256.Sum256([]byte("different synthetic membership")),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateV0330ReleaseEvidenceProjectionPair(test.metadata, test.bounded); err == nil {
				t.Fatal("invalid projection pair unexpectedly passed")
			}
		})
	}
}

// BenchmarkV0330CopiedStoreReleaseEvidence is intentionally opt-in. It creates
// a genuine multi-GiB pre-31 SQLite store from synthetic extents, mutates only
// a private copy, and emits a single metrics-only marker. The repo-tooling
// runner captures all other output privately and removes its scratch root.
func BenchmarkV0330CopiedStoreReleaseEvidence(b *testing.B) {
	if os.Getenv("TRACEARY_RUN_V0330_RELEASE_EVIDENCE") != "1" {
		b.Skip("set TRACEARY_RUN_V0330_RELEASE_EVIDENCE=1 to create the multi-GiB copied-store fixture")
	}
	b.StopTimer()
	ctx := context.Background()
	root := b.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	copyPath := filepath.Join(root, "copy.db")

	source := infra.NewDatabase(sourcePath, onDiskSQLiteMigrationsBefore(b, 31))
	if err := infra.NewStoreManagementDatasource(source).Initialize(ctx); err != nil {
		b.Fatalf("initialize pre-31 source: %v", err)
	}
	sourceDB, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		b.Fatalf("open pre-31 source: %v", err)
	}
	if err := seedV0330ReleaseEvidenceStore(ctx, sourceDB); err != nil {
		_ = sourceDB.Close()
		b.Fatalf("seed pre-31 source: %v", err)
	}
	if _, err := sourceDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = sourceDB.Close()
		b.Fatalf("checkpoint pre-31 source: %v", err)
	}
	sourceManagedBytes, sourceEvents, sourceStoredBytes, missingMetadata, err := v0330ReleaseEvidenceStoreExtent(ctx, sourceDB)
	if err != nil {
		_ = sourceDB.Close()
		b.Fatalf("inspect pre-31 source extent: %v", err)
	}
	if sourceManagedBytes < v0330ReleaseEvidenceMinimumDBBytes ||
		sourceEvents != v0330ReleaseEvidenceEvents ||
		sourceStoredBytes < v0330ReleaseEvidenceMinimumDBBytes ||
		missingMetadata != 0 {
		_ = sourceDB.Close()
		b.Fatalf(
			"pre-31 source extent is invalid: managed=%d events=%d stored=%d missing_metadata=%d",
			sourceManagedBytes,
			sourceEvents,
			sourceStoredBytes,
			missingMetadata,
		)
	}
	if err := sourceDB.Close(); err != nil {
		b.Fatalf("close pre-31 source: %v", err)
	}
	sourceDigestBefore, err := v0330ReleaseEvidenceFileDigest(sourcePath)
	if err != nil {
		b.Fatalf("digest pre-31 source: %v", err)
	}
	if err := v0330ReleaseEvidenceCopyFile(sourcePath, copyPath); err != nil {
		b.Fatalf("copy pre-31 source: %v", err)
	}

	current := infra.NewDatabase(copyPath, onDiskSQLiteMigrations(b))
	store := infra.NewStoreManagementDatasource(current)
	migrationStarted := time.Now()
	if err := store.Initialize(ctx); err != nil {
		b.Fatalf("initialize copied current store: %v", err)
	}
	migrationElapsed := time.Since(migrationStarted)

	copyDB, err := sql.Open("sqlite", copyPath)
	if err != nil {
		b.Fatalf("open copied current store: %v", err)
	}
	migrations31Through34, err := v0330ReleaseEvidenceMigrationsApplied(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect copied migrations: %v", err)
	}
	projectionRows, err := v0330ReleaseEvidenceProjectionRows(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect copied metadata projection: %v", err)
	}
	integrityOK, err := v0330ReleaseEvidenceIntegrityOK(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect copied integrity: %v", err)
	}
	foreignKeyViolations, err := v0330ReleaseEvidenceForeignKeyViolations(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect copied foreign keys: %v", err)
	}
	initialDocuments, initialComplete, err := v0330ReleaseEvidenceFTSProgress(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect initial FTS progress: %v", err)
	}
	if initialDocuments != 128 || initialComplete {
		_ = copyDB.Close()
		b.Fatalf("initial FTS progress = (%d,%v), want (128,false)", initialDocuments, initialComplete)
	}

	datasource := infra.NewEventDatasource(current)
	base := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	workspace := domtypes.Workspace("release-evidence")
	sessionID := domtypes.SessionID("release-session")
	listCriteria := apptypes.NewEventListCriteriaBuilder(v0330ReleaseEvidenceProbeLimit).
		Workspace(workspace).
		To(base.Add(v0330ReleaseEvidenceEvents * time.Second)).
		Build()
	contextCriteria := apptypes.NewEventContextCriteriaBuilder(v0330ReleaseEvidenceProbeLimit).
		Workspace(workspace).
		SessionID(sessionID).
		To(base.Add(v0330ReleaseEvidenceEvents * time.Second)).
		Build()
	searchCriteria := apptypes.NewEventSearchCriteriaBuilder(v0330ReleaseEvidenceProbeLimit).
		Query("release needle").
		Workspace(workspace).
		To(base.Add(v0330ReleaseEvidenceEvents * time.Second)).
		Build()

	probes := make([]v0330ReleaseEvidenceProbe, 0, 9)
	listMetadataProbe, listMetadata := measureV0330ReleaseEvidenceProbe(
		b, "list", "metadata", "not_applicable",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.ListRecentMetadata(ctx, listCriteria)
			return v0330ReleaseEvidenceMetadataSample(events), err
		},
	)
	probes = append(probes, listMetadataProbe)
	listBoundedProbe, listBounded := measureV0330ReleaseEvidenceProbe(
		b, "list", "bounded", "not_applicable",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.ListRecentBounded(ctx, listCriteria, 500)
			return v0330ReleaseEvidenceBoundedSample(events), err
		},
	)
	probes = append(probes, listBoundedProbe)
	if err := validateV0330ReleaseEvidenceProjectionPair(listMetadata, listBounded); err != nil {
		_ = copyDB.Close()
		b.Fatalf("list retrieval correctness gate failed: %v", err)
	}
	hugeCriteria := apptypes.NewEventListCriteriaBuilder(1).Workspace(workspace).From(base.Add(6500 * time.Millisecond)).To(base.Add(7500 * time.Millisecond)).Build()
	hugeProbe, _ := measureV0330ReleaseEvidenceProbe(b, "list", "bounded_huge", "not_applicable", func() (v0330ReleaseEvidenceSample, error) {
		events, err := datasource.ListRecentBounded(ctx, hugeCriteria, 500)
		return v0330ReleaseEvidenceBoundedSample(events), err
	})
	hugeProbe.SourceBodyBytes = v0330ReleaseEvidenceBodyBytes
	hugeProbe.BoundedBudgetBytes = 500
	if hugeProbe.ReturnedItems != 1 || hugeProbe.ReturnedBodyBytes <= 0 || hugeProbe.ReturnedBodyBytes > hugeProbe.BoundedBudgetBytes {
		b.Fatal("huge-body bounded probe violated its response budget")
	}
	probes = append(probes, hugeProbe)
	contextMetadataProbe, contextMetadata := measureV0330ReleaseEvidenceProbe(
		b, "context", "metadata", "not_applicable",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.GetContextMetadata(ctx, contextCriteria)
			return v0330ReleaseEvidenceMetadataSample(events), err
		},
	)
	probes = append(probes, contextMetadataProbe)
	contextBoundedProbe, contextBounded := measureV0330ReleaseEvidenceProbe(
		b, "context", "bounded", "not_applicable",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.GetContextBounded(ctx, contextCriteria, 500)
			return v0330ReleaseEvidenceBoundedSample(events), err
		},
	)
	probes = append(probes, contextBoundedProbe)
	if err := validateV0330ReleaseEvidenceProjectionPair(contextMetadata, contextBounded); err != nil {
		_ = copyDB.Close()
		b.Fatalf("context retrieval correctness gate failed: %v", err)
	}
	searchMetadataIncompleteProbe, searchMetadataIncomplete := measureV0330ReleaseEvidenceProbe(
		b, "search", "metadata", "incomplete",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.SearchMetadata(ctx, searchCriteria)
			return v0330ReleaseEvidenceMetadataSample(events), err
		},
	)
	probes = append(probes, searchMetadataIncompleteProbe)
	searchBoundedIncompleteProbe, searchBoundedIncomplete := measureV0330ReleaseEvidenceProbe(
		b, "search", "bounded", "incomplete",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.SearchBounded(ctx, searchCriteria, 500)
			return v0330ReleaseEvidenceBoundedSample(events), err
		},
	)
	probes = append(probes, searchBoundedIncompleteProbe)
	if err := validateV0330ReleaseEvidenceProjectionPair(
		searchMetadataIncomplete,
		searchBoundedIncomplete,
	); err != nil {
		_ = copyDB.Close()
		b.Fatalf("incomplete search retrieval correctness gate failed: %v", err)
	}

	resumeStarted := time.Now()
	if err := store.Initialize(ctx); err != nil {
		_ = copyDB.Close()
		b.Fatalf("resume copied FTS backfill: %v", err)
	}
	resumeElapsed := time.Since(resumeStarted)
	finalDocuments, finalComplete, err := v0330ReleaseEvidenceFTSProgress(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("inspect final FTS progress: %v", err)
	}
	if finalDocuments != v0330ReleaseEvidenceEvents || !finalComplete {
		_ = copyDB.Close()
		b.Fatalf("final FTS progress = (%d,%v), want (%d,true)", finalDocuments, finalComplete, v0330ReleaseEvidenceEvents)
	}
	preProjectionWriterOK, err := v0330ReleaseEvidencePreProjectionWriterOK(ctx, copyDB)
	if err != nil {
		_ = copyDB.Close()
		b.Fatalf("verify pre-projection writer compatibility: %v", err)
	}

	searchMetadataCompleteProbe, searchMetadataComplete := measureV0330ReleaseEvidenceProbe(
		b, "search", "metadata", "complete",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.SearchMetadata(ctx, searchCriteria)
			return v0330ReleaseEvidenceMetadataSample(events), err
		},
	)
	probes = append(probes, searchMetadataCompleteProbe)
	searchBoundedCompleteProbe, searchBoundedComplete := measureV0330ReleaseEvidenceProbe(
		b, "search", "bounded", "complete",
		func() (v0330ReleaseEvidenceSample, error) {
			events, err := datasource.SearchBounded(ctx, searchCriteria, 500)
			return v0330ReleaseEvidenceBoundedSample(events), err
		},
	)
	probes = append(probes, searchBoundedCompleteProbe)
	if err := validateV0330ReleaseEvidenceProjectionPair(
		searchMetadataComplete,
		searchBoundedComplete,
	); err != nil {
		_ = copyDB.Close()
		b.Fatalf("complete search retrieval correctness gate failed: %v", err)
	}
	if searchMetadataIncomplete.identity != searchMetadataComplete.identity ||
		searchBoundedIncomplete.identity != searchBoundedComplete.identity ||
		searchMetadataIncomplete.items != searchMetadataComplete.items ||
		searchBoundedIncomplete.items != searchBoundedComplete.items {
		_ = copyDB.Close()
		b.Fatal("incomplete and complete FTS phases returned different result membership")
	}

	if _, err := copyDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = copyDB.Close()
		b.Fatalf("checkpoint copied current store: %v", err)
	}
	if err := copyDB.Close(); err != nil {
		b.Fatalf("close copied current store: %v", err)
	}
	sourceDigestAfter, err := v0330ReleaseEvidenceFileDigest(sourcePath)
	if err != nil {
		b.Fatalf("digest source after copy migration: %v", err)
	}
	scratchBytesAfterCheckpoint, err := v0330ReleaseEvidenceDirectoryBytes(root)
	if err != nil {
		b.Fatalf("inspect scratch extent: %v", err)
	}

	evidence := v0330ReleaseEvidencePhaseBC{
		PhaseB: v0330ReleaseEvidencePhaseB{
			SourceManagedBytes:          sourceManagedBytes,
			ScratchBytesAfterCheckpoint: scratchBytesAfterCheckpoint,
			Events:                      sourceEvents,
			MigrationMS:                 float64(migrationElapsed) / float64(time.Millisecond),
			ResumeBackfillMS:            float64(resumeElapsed) / float64(time.Millisecond),
			Migrations31Through34:       migrations31Through34,
			ProjectionRows:              projectionRows,
			IntegrityOK:                 integrityOK,
			ForeignKeyViolations:        foreignKeyViolations,
			SourceUnchanged:             sourceDigestBefore == sourceDigestAfter,
			InitialFTSDocuments:         initialDocuments,
			InitialFTSComplete:          initialComplete,
			FinalFTSDocuments:           finalDocuments,
			FinalFTSComplete:            finalComplete,
			PreProjectionWriterOK:       preProjectionWriterOK,
		},
		PhaseC: probes,
	}
	if !evidence.PhaseB.Migrations31Through34 ||
		evidence.PhaseB.ProjectionRows != evidence.PhaseB.Events ||
		!evidence.PhaseB.IntegrityOK ||
		evidence.PhaseB.ForeignKeyViolations != 0 ||
		!evidence.PhaseB.SourceUnchanged || !evidence.PhaseB.PreProjectionWriterOK {
		b.Fatal("copied-store migration evidence failed its safety invariants")
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		b.Fatalf("marshal Phase-B/C evidence: %v", err)
	}
	b.ReportMetric(float64(sourceManagedBytes), "source_managed_bytes")
	b.ReportMetric(float64(scratchBytesAfterCheckpoint), "scratch_bytes_after_checkpoint")
	b.ReportMetric(float64(migrationElapsed)/float64(time.Millisecond), "migration_ms")
	b.Logf("TRACEARY_PHASE_BC_EVIDENCE=%s", encoded)
}

func seedV0330ReleaseEvidenceStore(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fixture transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	large, err := tx.PrepareContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_availability
		) VALUES (?, 'note', 'cli', 'codex', 'release-session', 'release-evidence',
			zeroblob(?), ?, 'available')`)
	if err != nil {
		return fmt.Errorf("prepare large fixture insert: %w", err)
	}
	small, err := tx.PrepareContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_availability
		) VALUES (?, 'note', 'cli', 'codex', 'release-session', 'release-evidence',
			?, ?, 'available')`)
	if err != nil {
		_ = large.Close()
		return fmt.Errorf("prepare small fixture insert: %w", err)
	}
	base := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	for index := 0; index < v0330ReleaseEvidenceEvents; index++ {
		eventID := fmt.Sprintf("release-event-%05d", index)
		createdAt := base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
		if index < v0330ReleaseEvidenceLargeEvents {
			if _, err := large.ExecContext(ctx, eventID, v0330ReleaseEvidenceBodyBytes, createdAt); err != nil {
				_ = large.Close()
				_ = small.Close()
				return fmt.Errorf("insert large fixture %d: %w", index, err)
			}
			continue
		}
		body := fmt.Sprintf("release needle visible 日本語 synthetic %03d", index)
		if _, err := small.ExecContext(ctx, eventID, body, createdAt); err != nil {
			_ = large.Close()
			_ = small.Close()
			return fmt.Errorf("insert small fixture %d: %w", index, err)
		}
	}
	if err := large.Close(); err != nil {
		_ = small.Close()
		return fmt.Errorf("close large fixture statement: %w", err)
	}
	if err := small.Close(); err != nil {
		return fmt.Errorf("close small fixture statement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fixture transaction: %w", err)
	}
	return nil
}

func v0330ReleaseEvidenceStoreExtent(ctx context.Context, db *sql.DB) (
	managedBytes int64,
	events int64,
	storedBodyBytes int64,
	missingMetadata int64,
	err error,
) {
	var pageCount, pageSize int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("read page count: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("read page size: %w", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(body_stored_bytes), 0),
		       COALESCE(SUM(CASE WHEN body_stored_bytes IS NULL THEN 1 ELSE 0 END), 0)
		  FROM events`,
	).Scan(&events, &storedBodyBytes, &missingMetadata); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("read fixture extent: %w", err)
	}
	return pageCount * pageSize, events, storedBodyBytes, missingMetadata, nil
}

func v0330ReleaseEvidenceMigrationsApplied(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM schema_migrations
		 WHERE version IN (31, 32, 33, 34)`,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("read copied migration versions: %w", err)
	}
	return count == 4, nil
}

func v0330ReleaseEvidenceProjectionRows(ctx context.Context, db *sql.DB) (int64, error) {
	var rows int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_metadata_projection").Scan(&rows); err != nil {
		return 0, fmt.Errorf("read copied metadata projection rows: %w", err)
	}
	return rows, nil
}

func v0330ReleaseEvidenceIntegrityOK(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return false, fmt.Errorf("run integrity check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	ok := false
	for rows.Next() {
		count++
		var result string
		if err := rows.Scan(&result); err != nil {
			return false, fmt.Errorf("read integrity result: %w", err)
		}
		ok = result == "ok"
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate integrity results: %w", err)
	}
	return count == 1 && ok, nil
}

func v0330ReleaseEvidenceForeignKeyViolations(ctx context.Context, db *sql.DB) (int64, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return 0, fmt.Errorf("run foreign-key check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var violations int64
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate foreign-key results: %w", err)
	}
	return violations, nil
}

func v0330ReleaseEvidenceFTSProgress(ctx context.Context, db *sql.DB) (int64, bool, error) {
	var documents int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_search_documents").Scan(&documents); err != nil {
		return 0, false, fmt.Errorf("read FTS document count: %w", err)
	}
	var complete int
	if err := db.QueryRowContext(ctx, `
		SELECT completed
		  FROM event_search_backfill_state
		 WHERE singleton = 1`,
	).Scan(&complete); err != nil {
		return 0, false, fmt.Errorf("read FTS completion state: %w", err)
	}
	return documents, complete != 0, nil
}

func v0330ReleaseEvidencePreProjectionWriterOK(ctx context.Context, db *sql.DB) (bool, error) {
	count := func(table string) (int64, error) {
		var result int64
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&result)
		if err != nil {
			return 0, fmt.Errorf("count %s: %w", table, err)
		}
		return result, nil
	}
	eventsBefore, err := count("events")
	if err != nil {
		return false, fmt.Errorf("count events before writer probe: %w", err)
	}
	projectionBefore, err := count("event_metadata_projection")
	if err != nil {
		return false, fmt.Errorf("count projection before writer probe: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin writer probe: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events (id, kind, client, agent, session_id, workspace, body, created_at, body_availability) VALUES ('release-writer-probe', 'note', 'cli', 'codex', 'release-session', 'release-evidence', 'release needle writer probe', '2026-07-26T00:02:10Z', 'available')`); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("run writer probe: %w", err)
	}
	var maintained int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM event_metadata_projection WHERE id = 'release-writer-probe'").Scan(&maintained); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("read writer projection: %w", err)
	}
	if err := tx.Rollback(); err != nil {
		return false, fmt.Errorf("rollback writer probe: %w", err)
	}
	eventsAfter, err := count("events")
	if err != nil {
		return false, fmt.Errorf("count events after writer probe: %w", err)
	}
	projectionAfter, err := count("event_metadata_projection")
	if err != nil {
		return false, fmt.Errorf("count projection after writer probe: %w", err)
	}
	return maintained == 1 && eventsBefore == eventsAfter && projectionBefore == projectionAfter, nil
}

func measureV0330ReleaseEvidenceProbe(
	b *testing.B,
	operation string,
	projection string,
	ftsPhase string,
	run func() (v0330ReleaseEvidenceSample, error),
) (v0330ReleaseEvidenceProbe, v0330ReleaseEvidenceSample) {
	b.Helper()
	durations := make([]time.Duration, 0, v0330ReleaseEvidenceMeasurementRuns)
	var first v0330ReleaseEvidenceSample
	for index := 0; index < v0330ReleaseEvidenceMeasurementRuns; index++ {
		started := time.Now()
		sample, err := run()
		durations = append(durations, time.Since(started))
		if err != nil {
			b.Fatalf("%s/%s/%s probe failed: %v", operation, projection, ftsPhase, err)
		}
		if index == 0 {
			first = sample
			continue
		}
		if sample != first {
			b.Fatalf("%s/%s/%s probe changed across runs", operation, projection, ftsPhase)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(len(durations)*95+99)/100-1]
	return v0330ReleaseEvidenceProbe{
		Operation:         operation,
		Projection:        projection,
		FTSPhase:          ftsPhase,
		Runs:              v0330ReleaseEvidenceMeasurementRuns,
		P95MS:             float64(p95) / float64(time.Millisecond),
		ReturnedItems:     first.items,
		ReturnedBodyBytes: first.bodyBytes,
	}, first
}

func validateV0330ReleaseEvidenceProjectionPair(
	metadata v0330ReleaseEvidenceSample,
	bounded v0330ReleaseEvidenceSample,
) error {
	if metadata.items != v0330ReleaseEvidenceProbeLimit ||
		bounded.items != v0330ReleaseEvidenceProbeLimit ||
		metadata.bodyBytes != 0 ||
		bounded.bodyBytes <= 0 ||
		metadata.identity != bounded.identity {
		return fmt.Errorf("metadata and bounded retrieval results violate the release-evidence contract")
	}
	return nil
}

func v0330ReleaseEvidenceMetadataSample(events []apptypes.EventMetadata) v0330ReleaseEvidenceSample {
	identities := make([]string, 0, len(events))
	for _, event := range events {
		identities = append(identities, event.EventID().String())
	}
	return v0330ReleaseEvidenceSample{
		items:    len(events),
		identity: v0330ReleaseEvidenceIdentity(identities),
	}
}

func v0330ReleaseEvidenceBoundedSample(events []apptypes.BoundedEvent) v0330ReleaseEvidenceSample {
	identities := make([]string, 0, len(events))
	bodyBytes := 0
	for _, event := range events {
		identities = append(identities, event.Metadata().EventID().String())
		bodyBytes += len(event.Body())
	}
	return v0330ReleaseEvidenceSample{
		items:     len(events),
		bodyBytes: bodyBytes,
		identity:  v0330ReleaseEvidenceIdentity(identities),
	}
}

func v0330ReleaseEvidenceIdentity(values []string) [sha256.Size]byte {
	hasher := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(hasher, "%d:", len(value))
		_, _ = io.WriteString(hasher, value)
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func v0330ReleaseEvidenceFileDigest(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- private synthetic benchmark path
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open synthetic store for digest: %w", err)
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("digest synthetic store: %w", err)
	}
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

func v0330ReleaseEvidenceCopyFile(source, destination string) error {
	input, err := os.Open(source) // #nosec G304 -- private synthetic benchmark path
	if err != nil {
		return fmt.Errorf("open synthetic source: %w", err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- private synthetic benchmark path
	if err != nil {
		return fmt.Errorf("create synthetic copy: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy synthetic store: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close synthetic copy: %w", err)
	}
	return nil
}

func v0330ReleaseEvidenceDirectoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk synthetic scratch: %w", err)
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect synthetic scratch entry: %w", err)
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure synthetic scratch: %w", err)
	}
	return total, nil
}
