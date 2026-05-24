package sqlite_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

const tracearyRepoRootEnv = "TRACEARY_REPO_ROOT"

var (
	sqliteMigrationDirMu sync.Mutex
	sqliteMigrationDir   string
)

// onDiskSQLiteMigrations returns the repository's on-disk migration set for
// tests that intentionally exercise full-schema compatibility.
func onDiskSQLiteMigrations(t testing.TB) fs.FS {
	t.Helper()
	return os.DirFS(onDiskSQLiteMigrationDir(t))
}

func onDiskSQLiteMigrationDir(t testing.TB) string {
	t.Helper()

	sqliteMigrationDirMu.Lock()
	defer sqliteMigrationDirMu.Unlock()

	if sqliteMigrationDir != "" {
		return sqliteMigrationDir
	}

	dir, err := resolveOnDiskSQLiteMigrationDir()
	if err != nil {
		t.Fatal(err)
	}
	sqliteMigrationDir = dir
	return sqliteMigrationDir
}

func resolveOnDiskSQLiteMigrationDir() (string, error) {
	candidates, candidateErrs := sqliteMigrationDirCandidates()
	searchErrs := append([]error(nil), candidateErrs...)
	for _, dir := range candidates {
		if err := validateSQLiteMigrationDir(dir); err != nil {
			searchErrs = append(searchErrs, err)
			continue
		}
		return dir, nil
	}
	if len(searchErrs) == 0 {
		return "", errors.New("no SQLite migration directory candidates available")
	}
	return "", fmt.Errorf("resolve SQLite migrations directory: %w", errors.Join(searchErrs...))
}

func sqliteMigrationDirCandidates() ([]string, []error) {
	var candidates []string
	var errs []error

	if root := os.Getenv(tracearyRepoRootEnv); root != "" {
		candidates = appendUniquePath(candidates, filepath.Join(root, "schema", "sqlite", "migrations"))
	}

	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		candidates = appendUniquePath(
			candidates,
			filepath.Join(filepath.Dir(file), "..", "..", "schema", "sqlite", "migrations"),
		)
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = appendUniquePath(candidates, filepath.Join(cwd, "..", "..", "schema", "sqlite", "migrations"))
		candidates = appendUniquePath(candidates, filepath.Join(cwd, "schema", "sqlite", "migrations"))
	} else {
		errs = append(errs, fmt.Errorf("get working directory: %w", err))
	}

	return candidates, errs
}

func appendUniquePath(paths []string, candidate string) []string {
	candidate = filepath.Clean(candidate)
	for _, path := range paths {
		if path == candidate {
			return paths
		}
	}
	return append(paths, candidate)
}

func validateSQLiteMigrationDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat SQLite migrations path %s: %w", dir, err)
	}
	if !info.IsDir() {
		return errors.New("SQLite migrations path is not a directory: " + dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read SQLite migrations in %s: %w", dir, err)
	}
	seenVersions := map[int]struct{}{}
	foundSQL := false
	foundInitialMigration := false
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
		if entry.Name() == "000001_init.sql" {
			foundInitialMigration = true
		}
	}
	if !foundSQL {
		return errors.New("SQLite migrations path has no .sql files: " + dir)
	}
	if !foundInitialMigration {
		return errors.New("SQLite migrations path is missing 000001_init.sql: " + dir)
	}
	return nil
}

func sqliteMigrationVersion(name string) (int, error) {
	versionText, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration filename %q missing version separator", name)
	}
	if len(versionText) != 6 {
		return 0, fmt.Errorf("migration filename %q must use a six-digit version prefix", name)
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
