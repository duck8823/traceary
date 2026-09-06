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
	"testing/fstest"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

const sqliteMigrationVersionDigits = 6

// onDiskSQLiteMigrations returns the repository's on-disk migration set for
// tests that intentionally exercise full-schema compatibility.
func onDiskSQLiteMigrations(t testing.TB) fs.FS {
	t.Helper()
	return os.DirFS(onDiskSQLiteMigrationDir(t))
}

// onDiskSQLiteMigrationsBefore returns a copied migration set ending before
// the requested version. It is used to create genuine pre-migration stores
// before reopening them with the complete set.
func onDiskSQLiteMigrationsBefore(t testing.TB, beforeVersion int) fs.FS {
	t.Helper()
	dir := onDiskSQLiteMigrationDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read SQLite migrations: %v", err)
	}
	migrations := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		version, err := sqliteMigrationVersion(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if version >= beforeVersion {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		migrations[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return migrations
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
	db := sqlite.NewDatabase(dbPath, withEventWriteSchema(t, migrations))
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
	db := sqlite.NewDatabase(dbPath, withEventWriteSchema(t, migrations))
	return sqlite.NewEventDatasource(db), sqlite.NewSessionDatasource(db), sqlite.NewStoreManagementDatasource(db)
}

// withEventWriteSchema appends Save-path tables onto skip-ahead MapFS
// that never create sessions. Catalogs that already DDL sessions (on-disk
// lineage or list-sessions fixtures) are left unchanged.
func withEventWriteSchema(t testing.TB, migrations fs.FS) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	hasSessionsDDL := false
	initPath := ""
	err := fs.WalkDir(migrations, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := fs.ReadFile(migrations, path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		out[path] = &fstest.MapFile{Data: data}
		base := filepath.Base(path)
		if strings.Contains(string(data), "CREATE TABLE sessions") || strings.Contains(string(data), "CREATE TABLE IF NOT EXISTS sessions") {
			hasSessionsDDL = true
		}
		if strings.HasPrefix(base, "000001_") {
			initPath = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copy migrations: %v", err)
	}
	if hasSessionsDDL || initPath == "" {
		return migrations
	}
	extra := []byte(`
CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    workspace TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS hook_delivery_attempts (
    delivery_record_id TEXT NOT NULL,
    attempted_event_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    attempt_origin TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    PRIMARY KEY (delivery_record_id, attempted_event_id)
);
CREATE TABLE IF NOT EXISTS session_workspace_observations (
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    observed_relationship TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT '',
    observation_kind TEXT NOT NULL,
    observation_count INTEGER NOT NULL DEFAULT 1,
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    observed_event_id TEXT,
    raw_workspace TEXT,
    delivery_record_id TEXT,
    attribution_fingerprint TEXT NOT NULL,
    diagnostic_reason TEXT NOT NULL DEFAULT '',
    observation_origin TEXT NOT NULL,
    PRIMARY KEY (session_id, workspace, observed_relationship, source_client, source_hook, observation_kind)
);
`)
	out[initPath] = &fstest.MapFile{Data: append(append([]byte{}, out[initPath].Data...), extra...)}
	return out
}

// newStoreManagementDatasource returns a StoreManagementDatasource.
func newStoreManagementDatasource(
	t testing.TB,
	dbPath string,
	migrations fs.FS,
) *sqlite.StoreManagementDatasource {
	t.Helper()
	return sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(dbPath, migrations))
}
