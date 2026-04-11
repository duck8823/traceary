package sqlite

import (
	"context"
	"log/slog"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

var _ port.StoreBackupCreator = (*Datasource)(nil)
var _ port.StoreBackupRestorer = (*Datasource)(nil)

// CreateBackup creates a backup of the SQLite DB.
func (d *Datasource) CreateBackup(ctx context.Context, outputPath string, overwrite bool) (err error) {
	sourcePath, destinationPath, err := validateDistinctDBPaths(d.dbPath, outputPath)
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

	db, err := d.openDB(ctx)
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
func (d *Datasource) RestoreBackup(ctx context.Context, inputPath string, overwrite bool) (err error) {
	sourcePath, destinationPath, err := validateDistinctDBPaths(inputPath, d.dbPath)
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
	if err := d.Initialize(ctx); err != nil {
		return xerrors.Errorf("failed to initialize store after restore: %w", err)
	}

	return nil
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
