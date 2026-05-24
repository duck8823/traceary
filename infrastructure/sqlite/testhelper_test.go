package sqlite_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// productionSQLiteMigrations returns the on-disk production migration set for
// tests that intentionally exercise full-schema compatibility.
func productionSQLiteMigrations(t testing.TB) fs.FS {
	t.Helper()
	return os.DirFS(productionSQLiteMigrationDir(t))
}

func productionSQLiteMigrationDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "schema", "sqlite", "migrations"))
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat production SQLite migrations dir %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("production SQLite migrations path %s is not a directory", dir)
	}
	return dir
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
