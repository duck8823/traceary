package sqlite_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// onDiskSQLiteMigrations returns the repository's on-disk migration set for
// tests that intentionally exercise full-schema compatibility.
func onDiskSQLiteMigrations(t testing.TB) fs.FS {
	t.Helper()
	return os.DirFS(onDiskSQLiteMigrationDir(t))
}

func onDiskSQLiteMigrationDir(t testing.TB) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	root, err := findTracearyRepositoryRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "schema", "sqlite", "migrations")
	if err := validateSQLiteMigrationDir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func findTracearyRepositoryRoot(start string) (string, error) {
	dir := filepath.Clean(start)
	for {
		goModPath := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(goModPath)
		switch {
		case err == nil:
			if bytes.Contains(data, []byte("module github.com/duck8823/traceary")) {
				return dir, nil
			}
			return "", errors.New("found go.mod with unexpected module at " + goModPath)
		case errors.Is(err, os.ErrNotExist):
			// Continue walking upward.
		default:
			return "", fmt.Errorf("read %s: %w", goModPath, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("traceary go.mod not found from " + start)
		}
		dir = parent
	}
}

func validateSQLiteMigrationDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat SQLite migrations path %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("SQLite migrations path is a symlink: " + dir)
	}
	if !info.IsDir() {
		return errors.New("SQLite migrations path is not a directory: " + dir)
	}
	if _, err := os.Lstat(filepath.Join(dir, "000001_init.sql")); err != nil {
		return fmt.Errorf("stat initial SQLite migration in %s: %w", dir, err)
	}
	return nil
}

// newEventDatasource returns an EventDatasource plus a matching
// StoreManagementDatasource for initialize/migrate operations.
func newEventDatasource(
	t *testing.T,
	dbPath string,
	migrations fs.FS,
) (*sqlite.EventDatasource, *sqlite.StoreManagementDatasource) {
	t.Helper()
	db := sqlite.NewDatabase(dbPath, migrations)
	return sqlite.NewEventDatasource(db), sqlite.NewStoreManagementDatasource(db)
}

// newFullDatasources returns both EventDatasource and SessionDatasource
// backed by the same Database for tests that exercise cross-aggregate
// behaviour such as FindLatest over saved events.
func newFullDatasources(
	t *testing.T,
	dbPath string,
	migrations fs.FS,
) (*sqlite.EventDatasource, *sqlite.SessionDatasource, *sqlite.StoreManagementDatasource) {
	t.Helper()
	db := sqlite.NewDatabase(dbPath, migrations)
	return sqlite.NewEventDatasource(db), sqlite.NewSessionDatasource(db), sqlite.NewStoreManagementDatasource(db)
}

// newStoreManagementDatasource returns a StoreManagementDatasource.
func newStoreManagementDatasource(
	t *testing.T,
	dbPath string,
	migrations fs.FS,
) *sqlite.StoreManagementDatasource {
	t.Helper()
	return sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(dbPath, migrations))
}
