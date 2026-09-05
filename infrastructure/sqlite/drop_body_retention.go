package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

// SemanticVerifierDropBodyRetention is the Layer-2 verifier for offline
// migration 83 (owner #2325). It asserts column/table absence only.
const SemanticVerifierDropBodyRetention SemanticVerifierID = "drop_body_retention"

const droppedBodyRetentionReaderVersion = 39

const bodyRetentionCandidateIndex = "idx_events_raw_body_retention_candidates"

var droppedBodyRetentionTables = []string{
	"raw_body_retention_entries",
	"raw_body_retention_executions",
	"raw_body_retention_store_identity",
	"session_orphan_ranges",
}

func pendingDropsBodyRetentionObjects(plan PreparedMigrationPlan) bool {
	for _, migration := range plan.Pending {
		if migration.Version != 83 {
			continue
		}
		entry, ok := preparedMigrationManifest[83]
		if ok && entry.SemanticVerifierID == SemanticVerifierDropBodyRetention {
			return true
		}
	}
	return false
}

func inspectUnavailableRetention(ctx context.Context, db *sql.DB) (int64, string, []string, int64, error) {
	hasColumn, err := tableHasColumn(ctx, db, "events", "body_availability")
	if err != nil {
		return 0, "", nil, 0, err
	}
	schemaVersion, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return 0, "", nil, 0, err
	}
	if !hasColumn {
		return 0, hex.EncodeToString(sha256.New().Sum(nil)), nil, schemaVersion, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM events WHERE body_availability = 'unavailable_retention' ORDER BY id`)
	if err != nil {
		return 0, "", nil, 0, xerrors.Errorf("inspect unavailable_retention identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hasher := sha256.New()
	var count int64
	sample := make([]string, 0, apptypes.UnavailableRetentionInlineIDBound)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, "", nil, 0, xerrors.Errorf("scan unavailable_retention identity: %w", err)
		}
		_, _ = hasher.Write([]byte(id))
		_, _ = hasher.Write([]byte{'\n'})
		if int64(len(sample)) < apptypes.UnavailableRetentionInlineIDBound {
			sample = append(sample, id)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", nil, 0, xerrors.Errorf("iterate unavailable_retention identities: %w", err)
	}
	return count, hex.EncodeToString(hasher.Sum(nil)), sample, schemaVersion, nil
}

func currentSchemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var version int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, xerrors.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func unavailableRetentionApprovalRequired(count int64, digest string, sample []string, schemaVersion int64) error {
	return &apptypes.UnavailableRetentionApprovalRequiredError{
		RowCount:      count,
		Digest:        digest,
		Sample:        sample,
		SchemaVersion: schemaVersion,
	}
}

func (d *Database) inspectUnavailableRetentionAt(ctx context.Context, snapshot string) (apptypes.UnavailableRetentionInspection, error) {
	if snapshot == "" {
		return apptypes.UnavailableRetentionInspection{Digest: hex.EncodeToString(sha256.New().Sum(nil))}, nil
	}
	db, err := openDirectReadOnly(ctx, snapshot)
	if err != nil {
		return apptypes.UnavailableRetentionInspection{}, xerrors.Errorf("open store for unavailable retention inspect: %w", err)
	}
	defer func() { _ = db.Close() }()
	count, digest, sample, schemaVersion, err := inspectUnavailableRetention(ctx, db)
	if err != nil {
		return apptypes.UnavailableRetentionInspection{}, err
	}
	return apptypes.UnavailableRetentionInspection{
		RowCount:      count,
		Digest:        digest,
		Sample:        sample,
		SchemaVersion: schemaVersion,
	}, nil
}

// VerifyUnavailableRetention re-reads the source identity set immediately before Build.
func (r *PreparedUpgradeMigrationRecipe) VerifyUnavailableRetention(ctx context.Context, request application.PreparedCandidateRequest) error {
	db, err := openDirectReadOnly(ctx, request.Run.SourcePath)
	if err != nil {
		return fmt.Errorf("open source for unavailable retention preflight: %w", err)
	}
	defer func() { _ = db.Close() }()
	count, digest, sample, schemaVersion, err := inspectUnavailableRetention(ctx, db)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if request.Run.UnavailableRetentionApproval == nil {
		return unavailableRetentionApprovalRequired(count, digest, sample, schemaVersion)
	}
	if !request.Run.UnavailableRetentionApproval.Matches(
		request.Run.ID,
		request.Run.SourceIdentity,
		schemaVersion,
		count,
		digest,
	) {
		return unavailableRetentionApprovalRequired(count, digest, sample, schemaVersion)
	}
	return nil
}

func verifyDropBodyRetention(ctx context.Context, candidateDB *sql.DB) error {
	hasColumn, err := tableHasColumn(ctx, candidateDB, "events", "body_availability")
	if err != nil {
		return err
	}
	if hasColumn {
		return fmt.Errorf("candidate events still has column body_availability")
	}
	for _, name := range droppedBodyRetentionTables {
		exists, err := tableExists(ctx, candidateDB, name)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("candidate still has table %s", name)
		}
	}
	indexExists, err := sqliteIndexExists(ctx, candidateDB, bodyRetentionCandidateIndex)
	if err != nil {
		return err
	}
	if indexExists {
		return fmt.Errorf("candidate still has index %s", bodyRetentionCandidateIndex)
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if minimumReader != droppedBodyRetentionReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want %d", minimumReader, droppedBodyRetentionReaderVersion)
	}
	return verifyNoBodyAvailabilityReferences(ctx, candidateDB)
}

func verifyNoBodyAvailabilityReferences(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("list candidate schema objects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, sqlText string
		if err := rows.Scan(&name, &sqlText); err != nil {
			return fmt.Errorf("scan candidate schema object: %w", err)
		}
		if strings.Contains(sqlText, "body_availability") {
			return fmt.Errorf("candidate object %s still references body_availability", name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate candidate schema objects: %w", err)
	}
	return nil
}

var _ application.UnavailableRetentionVerifier = (*PreparedUpgradeMigrationRecipe)(nil)
