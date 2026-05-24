package sqlite_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

const sqliteMigrationVersionDigits = 6

// onDiskSQLiteMigrations returns the repository's on-disk migration set for
// tests that intentionally exercise full-schema compatibility.
func onDiskSQLiteMigrations(t testing.TB) fs.FS {
	t.Helper()
	return os.DirFS(onDiskSQLiteMigrationDir(t))
}

func onDiskSQLiteMigrationDir(t testing.TB) string {
	t.Helper()

	dir, err := resolveOnDiskSQLiteMigrationDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func resolveOnDiskSQLiteMigrationDir() (string, error) {
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		dir := filepath.Join(filepath.Dir(file), "..", "..", "schema", "sqlite", "migrations")
		if err := validateSQLiteMigrationDir(dir); err != nil {
			return "", err
		}
		return dir, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	dir := filepath.Join(cwd, "..", "..", "schema", "sqlite", "migrations")
	if err := validateSQLiteMigrationDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func validateSQLiteMigrationDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat SQLite migrations path %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("SQLite migrations path is not a directory: %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read SQLite migrations in %s: %w", dir, err)
	}
	seenVersions := map[int]struct{}{}
	foundSQL := false
	foundVersionOne := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		foundSQL = true
		version, err := sqliteMigrationVersion(entry.Name())
		if err != nil {
			return err
		}
		if _, exists := seenVersions[version]; exists {
			return fmt.Errorf("duplicate SQLite migration version %d in %s", version, dir)
		}
		seenVersions[version] = struct{}{}
		if version == 1 {
			foundVersionOne = true
		}
	}
	if !foundSQL {
		return fmt.Errorf("SQLite migrations path has no .sql files: %s", dir)
	}
	if !foundVersionOne {
		return fmt.Errorf("SQLite migrations path is missing migration version 1: %s", dir)
	}
	return nil
}

func sqliteMigrationVersion(name string) (int, error) {
	versionText, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration filename %q missing version separator", name)
	}
	if len(versionText) != sqliteMigrationVersionDigits {
		return 0, fmt.Errorf("migration filename %q must use a %d-digit version prefix", name, sqliteMigrationVersionDigits)
	}
	for _, digit := range versionText {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("migration filename %q has non-numeric version prefix", name)
		}
	}
	version, err := strconv.Atoi(versionText)
	if err != nil {
		return 0, fmt.Errorf("migration filename %q has invalid version: %w", name, err)
	}
	return version, nil
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
