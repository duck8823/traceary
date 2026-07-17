package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/delete_old_events.sql
var deleteOldEventsQuery string

//go:embed sql/delete_empty_sessions.sql
var deleteEmptySessionsQuery string

//go:embed sql/clear_deleted_memory_supersedes_refs.sql
var clearDeletedMemorySupersedesRefsQuery string

//go:embed sql/delete_old_memories.sql
var deleteOldMemoriesQuery string

//go:embed sql/delete_stale_extracted_candidates.sql
var deleteStaleExtractedCandidatesQuery string

//go:embed sql/clear_stale_extracted_candidate_supersedes_refs.sql
var clearStaleExtractedCandidateSupersedesRefsQuery string

// staleExtractedCandidateRetention is the fixed retention window
// applied to candidate memories with `source IN (extracted,
// extracted-hidden, compact-summary)`. The default operator-controlled cutoff
// (`--keep-days`) protects long-lived facts; this shorter window
// applies only to auto-extracted candidates that were never reviewed.
// Untouched extracted candidates older than this are decayed to
// status=expired (not hard-deleted) on the next gc pass that targets
// `memories` or `all`, so operators can restore until keep-days GC.
// Tracked under #1368 / v0.11.0 sub-issue #832.
const staleExtractedCandidateRetention = 14 * 24 * time.Hour

//go:embed sql/delete_old_memory_edges.sql
var deleteOldMemoryEdgesQuery string

//go:embed sql/count_stale_sessions.sql
var countStaleSessionsQuery string

//go:embed sql/update_stale_sessions.sql
var updateStaleSessionsQuery string

// StoreManagementDatasource provides store lifecycle and maintenance
// operations backed by SQLite.
type StoreManagementDatasource struct {
	db *Database
}

// NewStoreManagementDatasource creates a new StoreManagementDatasource
// bound to the given database.
func NewStoreManagementDatasource(db *Database) *StoreManagementDatasource {
	return &StoreManagementDatasource{db: db}
}

// Compile-time interface assertion.
var _ application.StoreManager = (*StoreManagementDatasource)(nil)

// Initialize creates the store directory, applies migrations, and sets
// file permissions.
func (d *StoreManagementDatasource) Initialize(ctx context.Context) error {
	return d.db.initialize(ctx)
}

// CreateBackup creates a backup of the SQLite DB.
func (d *StoreManagementDatasource) CreateBackup(ctx context.Context, outputPath string, overwrite bool) (err error) {
	// Snapshot the current DB path up front so a concurrent SetPath
	// cannot split the path we validate and the path we actually open.
	sourceSnapshot := d.db.Path()
	sourcePath, destinationPath, err := validateDistinctDBPaths(sourceSnapshot, outputPath)
	if err != nil {
		return xerrors.Errorf("failed to validate backup paths: %w", err)
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return xerrors.Errorf("failed to stat source DB: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return xerrors.Errorf("source DB must be a regular file")
	}
	if err := ensureParentDir(destinationPath); err != nil {
		return xerrors.Errorf("failed to prepare backup output directory: %w", err)
	}
	if err := prepareBackupCreateDestination(destinationPath, overwrite); err != nil {
		return xerrors.Errorf("failed to prepare backup output path: %w", err)
	}

	db, err := d.db.openAt(ctx, sourceSnapshot)
	if err != nil {
		return xerrors.Errorf("failed to open source DB for backup: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close source DB after backup: %w", closeErr)
		}
	}()

	statement := fmt.Sprintf("VACUUM INTO %s", quoteSQLiteStringLiteral(destinationPath))
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return xerrors.Errorf("failed to create SQLite backup: %w", err)
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		return xerrors.Errorf("failed to set backup file permissions: %w", err)
	}

	return nil
}

// RestoreBackup restores the SQLite DB from a backup file.
func (d *StoreManagementDatasource) RestoreBackup(ctx context.Context, inputPath string, overwrite bool) (err error) {
	// Snapshot the current DB path up front so a concurrent SetPath
	// cannot redirect the restore mid-flight.
	destinationSnapshot := d.db.Path()
	sourcePath, destinationPath, err := validateDistinctDBPaths(inputPath, destinationSnapshot)
	if err != nil {
		return xerrors.Errorf("failed to validate restore paths: %w", err)
	}
	inputInfo, err := os.Stat(sourcePath)
	if err != nil {
		return xerrors.Errorf("failed to stat backup input file: %w", err)
	}
	if !inputInfo.Mode().IsRegular() {
		return xerrors.Errorf("backup input file must be a regular file")
	}
	if err := ensureParentDir(destinationPath); err != nil {
		return xerrors.Errorf("failed to prepare restore directory: %w", err)
	}
	if err := prepareRestoreDestination(destinationPath, overwrite); err != nil {
		return xerrors.Errorf("failed to prepare restore destination: %w", err)
	}

	restoredTempPath, err := copyFileToTempPath(sourcePath, filepath.Dir(destinationPath))
	if err != nil {
		return xerrors.Errorf("failed to copy backup file: %w", err)
	}
	defer func() {
		if err != nil {
			if err := os.Remove(restoredTempPath); err != nil {
				slog.Debug("failed to remove file", "path", restoredTempPath, "error", err)
			}
		}
	}()

	cleanupOldDestination := func() error { return nil }
	rollbackOldDestination := func() error { return nil }
	if overwrite {
		cleanupOldDestination, rollbackOldDestination, err = stageRestoreDestination(destinationPath)
		if err != nil {
			return xerrors.Errorf("failed to stage existing restore destination: %w", err)
		}
		defer func() {
			if err != nil {
				if rollbackErr := rollbackOldDestination(); rollbackErr != nil {
					err = xerrors.Errorf("failed to roll back after restore failure: %w (original error: %v)", rollbackErr, err)
				}
				return
			}
			if cleanupErr := cleanupOldDestination(); cleanupErr != nil {
				err = xerrors.Errorf("failed to clean up staged restore backup: %w", cleanupErr)
			}
		}()
	}

	if err := os.Rename(restoredTempPath, destinationPath); err != nil {
		return xerrors.Errorf("failed to place restored DB file: %w", err)
	}
	for _, candidate := range []string{destinationPath + "-wal", destinationPath + "-shm"} {
		if err := removeFileIfExists(candidate); err != nil {
			return xerrors.Errorf("failed to remove restore sidecar file: %w", err)
		}
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		return xerrors.Errorf("failed to set restored DB file permissions: %w", err)
	}
	// Re-initialize using the snapshot captured at the top of this
	// function so a racing SetPath cannot redirect the post-restore
	// migration to a different database than the one we just placed.
	if err := d.db.initializeAt(ctx, destinationSnapshot); err != nil {
		return xerrors.Errorf("failed to initialize store after restore: %w", err)
	}

	return nil
}

// CollectGarbage removes or decays store records older than the given time for
// the selected target. Stale auto-extracted memory candidates are transitioned
// to expired (counted in the return value) rather than hard-deleted.
func (d *StoreManagementDatasource) CollectGarbage(
	ctx context.Context,
	before time.Time,
	target apptypes.GarbageCollectionTarget,
	dryRun bool,
) (int, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for garbage collection: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, xerrors.Errorf("failed to begin garbage-collection transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	deleteCount, err := d.collectGarbageInTx(ctx, tx, before, target)
	if err != nil {
		return 0, xerrors.Errorf("failed to collect garbage: %w", err)
	}
	if dryRun {
		return deleteCount, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, xerrors.Errorf("failed to commit garbage-collection transaction: %w", err)
	}
	committed = true

	if deleteCount > 0 {
		if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
			return 0, xerrors.Errorf("failed to run VACUUM: %w", err)
		}
	}

	return deleteCount, nil
}

func (d *StoreManagementDatasource) collectGarbageInTx(
	ctx context.Context,
	tx interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	},
	before time.Time,
	target apptypes.GarbageCollectionTarget,
) (int, error) {
	beforeValue := formatTimestamp(before)
	memoryEdgeBeforeValue := formatMemoryValidityTimestamp(before)

	if _, ok := apptypes.GarbageCollectionTargetFrom(target.String()); !ok {
		return 0, xerrors.Errorf("unsupported garbage-collection target: %s", target)
	}

	total := 0
	if target == apptypes.GarbageCollectionTargetEvents || target == apptypes.GarbageCollectionTargetAll {
		count, err := execRowsAffected(ctx, tx, deleteOldEventsQuery, beforeValue)
		if err != nil {
			return 0, xerrors.Errorf("failed to delete old events: %w", err)
		}
		total += count
	}
	if target == apptypes.GarbageCollectionTargetSessions || target == apptypes.GarbageCollectionTargetAll {
		count, err := execRowsAffected(ctx, tx, deleteEmptySessionsQuery, beforeValue)
		if err != nil {
			return 0, xerrors.Errorf("failed to delete empty sessions: %w", err)
		}
		total += count
	}
	if target == apptypes.GarbageCollectionTargetMemories || target == apptypes.GarbageCollectionTargetAll {
		if _, err := tx.ExecContext(ctx, clearDeletedMemorySupersedesRefsQuery, beforeValue); err != nil {
			return 0, xerrors.Errorf("failed to clear deleted memory supersedes references: %w", err)
		}
		count, err := execRowsAffected(ctx, tx, deleteOldMemoriesQuery, beforeValue)
		if err != nil {
			return 0, xerrors.Errorf("failed to delete old memories: %w", err)
		}
		total += count
		// Decay (not hard-delete) stale auto-extracted candidates the
		// operator never reviewed. Rows stay restorable as expired until
		// keep-days physical GC. See #1368 / #810/#832.
		extractedCutoff := formatTimestamp(time.Now().Add(-staleExtractedCandidateRetention))
		// Clear supersedes_memory_id references that point at candidates
		// about to leave the candidate status first so unusual operator
		// graphs cannot trip a foreign-key constraint.
		if _, err := tx.ExecContext(ctx, clearStaleExtractedCandidateSupersedesRefsQuery, extractedCutoff); err != nil {
			return 0, xerrors.Errorf("failed to clear stale extracted candidate supersedes references: %w", err)
		}
		extractedCount, err := execRowsAffected(ctx, tx, deleteStaleExtractedCandidatesQuery, extractedCutoff)
		if err != nil {
			return 0, xerrors.Errorf("failed to decay stale extracted candidates: %w", err)
		}
		total += extractedCount
	}
	if target == apptypes.GarbageCollectionTargetMemoryEdges || target == apptypes.GarbageCollectionTargetAll {
		count, err := execRowsAffected(ctx, tx, deleteOldMemoryEdgesQuery, memoryEdgeBeforeValue)
		if err != nil {
			return 0, xerrors.Errorf("failed to delete old memory edges: %w", err)
		}
		total += count
	}

	return total, nil
}

func execRowsAffected(
	ctx context.Context,
	executor interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	},
	query string,
	args ...any,
) (int, error) {
	result, err := executor.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, xerrors.Errorf("failed to execute deletion query: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, xerrors.Errorf("failed to check rows affected: %w", err)
	}
	return int(rowsAffected), nil
}

// CloseStaleSessions closes active sessions that have no recent events.
func (d *StoreManagementDatasource) CloseStaleSessions(
	ctx context.Context,
	staleAfter time.Duration,
	dryRun bool,
	protectedSessionIDs []types.SessionID,
) (int, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	cutoff := formatTimestamp(time.Now().Add(-staleAfter))
	countQuery, protectedArgs := staleSessionsQueryWithProtection(countStaleSessionsQuery, "s.session_id", protectedSessionIDs)
	updateQuery, _ := staleSessionsQueryWithProtection(updateStaleSessionsQuery, "session_id", protectedSessionIDs)

	if dryRun {
		args := append(protectedArgs, cutoff, cutoff)
		var count int
		if err := db.QueryRowContext(
			ctx,
			countQuery,
			args...,
		).Scan(&count); err != nil {
			return 0, xerrors.Errorf("failed to count stale sessions: %w", err)
		}
		return count, nil
	}

	now := formatTimestamp(time.Now())
	args := append([]any{now}, protectedArgs...)
	args = append(args, cutoff, cutoff)
	result, err := db.ExecContext(
		ctx,
		updateQuery,
		args...,
	)
	if err != nil {
		return 0, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, xerrors.Errorf("failed to check rows affected: %w", err)
	}

	return int(rowsAffected), nil
}

func staleSessionsQueryWithProtection(query string, sessionIDColumn string, protectedSessionIDs []types.SessionID) (string, []any) {
	const marker = "/* protected sessions */"
	seen := make(map[types.SessionID]struct{}, len(protectedSessionIDs))
	args := make([]any, 0, len(protectedSessionIDs))
	placeholders := make([]string, 0, len(protectedSessionIDs))
	for _, sessionID := range protectedSessionIDs {
		if sessionID == "" {
			continue
		}
		if _, ok := seen[sessionID]; ok {
			continue
		}
		seen[sessionID] = struct{}{}
		placeholders = append(placeholders, "?")
		args = append(args, sessionID.String())
	}
	predicate := ""
	if len(placeholders) > 0 {
		predicate = "AND " + sessionIDColumn + " NOT IN (" + strings.Join(placeholders, ", ") + ")"
	}
	return strings.Replace(query, marker, predicate, 1), args
}

func validateDistinctDBPaths(firstPath string, secondPath string) (string, string, error) {
	trimmedFirstPath := strings.TrimSpace(firstPath)
	if trimmedFirstPath == "" {
		return "", "", xerrors.Errorf("path must not be empty")
	}
	trimmedSecondPath := strings.TrimSpace(secondPath)
	if trimmedSecondPath == "" {
		return "", "", xerrors.Errorf("path must not be empty")
	}

	resolvedFirstPath, err := filepath.Abs(trimmedFirstPath)
	if err != nil {
		return "", "", xerrors.Errorf("failed to resolve absolute path: %w", err)
	}
	resolvedSecondPath, err := filepath.Abs(trimmedSecondPath)
	if err != nil {
		return "", "", xerrors.Errorf("failed to resolve absolute path: %w", err)
	}
	if resolvedFirstPath == resolvedSecondPath {
		return "", "", xerrors.Errorf("paths must be different")
	}

	return resolvedFirstPath, resolvedSecondPath, nil
}

func ensureParentDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return xerrors.Errorf("failed to create parent directory: %w", err)
	}

	return nil
}

func prepareBackupCreateDestination(path string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return xerrors.Errorf("output path already exists")
		} else if !os.IsNotExist(err) {
			return xerrors.Errorf("failed to inspect output path: %w", err)
		}

		return nil
	}

	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := removeFileIfExists(candidate); err != nil {
			return xerrors.Errorf("failed to remove existing file: %w", err)
		}
	}

	return nil
}

func prepareRestoreDestination(path string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return xerrors.Errorf("output path already exists")
		} else if !os.IsNotExist(err) {
			return xerrors.Errorf("failed to inspect output path: %w", err)
		}
	}

	return nil
}

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to remove file: %w", err)
	}

	return nil
}

func copyFileToTempPath(sourcePath string, destinationDir string) (_ string, err error) {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return "", xerrors.Errorf("failed to open input file: %w", err)
	}
	defer func() {
		if closeErr := sourceFile.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close input file: %w", closeErr)
		}
	}()

	tempFile, err := os.CreateTemp(destinationDir, "traceary-restore-*.db")
	if err != nil {
		return "", xerrors.Errorf("failed to create temporary file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if err == nil {
			return
		}
		if err := tempFile.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
		if err := os.Remove(tempPath); err != nil {
			slog.Debug("failed to remove file", "path", tempPath, "error", err)
		}
	}()

	if err := tempFile.Chmod(0o600); err != nil {
		return "", xerrors.Errorf("failed to set temporary file permissions: %w", err)
	}
	if _, err := io.Copy(tempFile, sourceFile); err != nil {
		return "", xerrors.Errorf("failed to copy input file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return "", xerrors.Errorf("failed to sync temporary file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return "", xerrors.Errorf("failed to close temporary file: %w", err)
	}

	return tempPath, nil
}

func stageRestoreDestination(path string) (func() error, func() error, error) {
	candidates := []string{path, path + "-wal", path + "-shm"}
	backups := map[string]string{}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, xerrors.Errorf("failed to inspect existing file: %w", err)
		}

		backupPath, err := reserveTempPath(filepath.Dir(candidate), filepath.Base(candidate)+".traceary-restore-old-*")
		if err != nil {
			return nil, nil, xerrors.Errorf("failed to reserve temporary backup path: %w", err)
		}
		if err := os.Rename(candidate, backupPath); err != nil {
			_ = restoreRenamedFiles(backups)
			return nil, nil, xerrors.Errorf("failed to move existing file aside: %w", err)
		}
		backups[candidate] = backupPath
	}

	cleanup := func() error {
		for _, backupPath := range backups {
			if err := removeFileIfExists(backupPath); err != nil {
				return xerrors.Errorf("failed to remove staged backup file: %w", err)
			}
		}

		return nil
	}
	rollback := func() error {
		return restoreRenamedFiles(backups)
	}

	return cleanup, rollback, nil
}

func restoreRenamedFiles(backups map[string]string) error {
	candidates := []string{}
	for originalPath := range backups {
		candidates = append(candidates, originalPath)
	}
	sort.Strings(candidates)

	for index := len(candidates) - 1; index >= 0; index-- {
		originalPath := candidates[index]
		backupPath := backups[originalPath]
		if err := removeFileIfExists(originalPath); err != nil {
			return xerrors.Errorf("failed to remove existing file before restore: %w", err)
		}
		if err := os.Rename(backupPath, originalPath); err != nil {
			return xerrors.Errorf("failed to restore staged backup file: %w", err)
		}
	}

	return nil
}

func reserveTempPath(dir string, pattern string) (string, error) {
	tempFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", xerrors.Errorf("failed to reserve temporary file path: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return "", xerrors.Errorf("failed to close temporary file: %w", err)
	}

	return tempPath, nil
}

func quoteSQLiteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
