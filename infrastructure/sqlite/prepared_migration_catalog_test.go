package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuildPreparedMigrationPlanClassifiesExactSuffix(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/store.db"
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 35))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteImmutableDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	plan, err := BuildPreparedMigrationPlan(ctx, db, preparedMigrations(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Current != 34 || plan.Latest != 45 || len(plan.Pending) != 11 || !plan.Offline || len(plan.Digest) != 64 {
		t.Fatalf("plan = %+v", plan)
	}
	want := map[int64]MigrationExecutionClass{
		35: MigrationDataDependentOffline,
		36: MigrationConstantInPlace,
		37: MigrationConstantInPlace,
		38: MigrationConstantInPlace,
		39: MigrationConstantInPlace,
		40: MigrationConstantInPlace,
		41: MigrationDataDependentOffline,
		42: MigrationDataDependentOffline,
		43: MigrationConstantInPlace,
		44: MigrationConstantInPlace,
		45: MigrationConstantInPlace,
	}
	for _, migration := range plan.Pending {
		if migration.Class != want[migration.Version] {
			t.Fatalf("migration %d class = %q", migration.Version, migration.Class)
		}
	}
}

func TestBuildPreparedMigrationPlanRejectsChangedManifestBody(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/store.db"
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 35))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	changed := cloneMigrationFS(t, preparedMigrations(t))
	changed["000035_add_session_lookup_projection_index.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	db, err := sql.Open("sqlite", sqliteImmutableDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = BuildPreparedMigrationPlan(ctx, db, changed); err == nil {
		t.Fatal("plan accepted a migration body that does not match the reviewed manifest")
	}
}

func TestBuildPreparedMigrationPlanRejectsCatalogGap(t *testing.T) {
	migrations := cloneMigrationFS(t, preparedMigrations(t))
	delete(migrations, "000020_add_session_model.sql")
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = BuildPreparedMigrationPlan(context.Background(), db, migrations); err == nil || !strings.Contains(err.Error(), "catalog gap") {
		t.Fatalf("BuildPreparedMigrationPlan() error = %v", err)
	}
}

func TestExecuteExactMigrationRejectsCatalogIdentityDrift(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = ensureSchemaMigrationsTable(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	body := []byte(`CREATE TABLE exact_catalog_test (id INTEGER PRIMARY KEY);`)
	sum := sha256.Sum256(body)
	valid := embeddedMigration{
		version: 1,
		name:    "000001_exact.sql",
		body:    body,
		digest:  hex.EncodeToString(sum[:]),
	}
	tests := map[string]embeddedMigration{
		"version": {version: 2, name: valid.name, body: valid.body, digest: valid.digest},
		"name":    {version: 1, name: "nested/000001_exact.sql", body: valid.body, digest: valid.digest},
		"digest":  {version: 1, name: valid.name, body: valid.body, digest: strings.Repeat("0", 64)},
	}
	for name, migration := range tests {
		t.Run(name, func(t *testing.T) {
			if err := executeExactMigration(context.Background(), db, migration); err == nil {
				t.Fatal("executeExactMigration() error = nil")
			}
		})
	}

	var created int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='exact_catalog_test'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("table created after rejected exact catalog entry: %d", created)
	}
}

func TestDatabaseMigrateUsesExactCatalogExecutor(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_exact.sql": {Data: []byte(`CREATE TABLE exact_catalog_test (id INTEGER PRIMARY KEY);`)},
	}
	database := NewDatabase(filepath.Join(t.TempDir(), "traceary.db"), migrations)
	if err := NewStoreManagementDatasource(database).Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(database.Path()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var name string
	if err = db.QueryRow(`SELECT name FROM schema_migrations WHERE version=1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "000001_exact.sql" {
		t.Fatalf("migration name = %q", name)
	}
}

func preparedMigrations(t *testing.T) fs.FS {
	t.Helper()
	dir, err := filepath.Abs("../../schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	return os.DirFS(dir)
}

func preparedMigrationsBefore(t *testing.T, before int64) fs.FS {
	t.Helper()
	source := preparedMigrations(t)
	paths, err := fs.Glob(source, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	result := fstest.MapFS{}
	for _, path := range paths {
		version, parseErr := parseMigrationVersion(path)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if version >= before {
			continue
		}
		body, readErr := fs.ReadFile(source, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		result[path] = &fstest.MapFile{Data: body}
	}
	return result
}

func cloneMigrationFS(t *testing.T, source fs.FS) fstest.MapFS {
	t.Helper()
	result := fstest.MapFS{}
	paths, err := fs.Glob(source, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		body, readErr := fs.ReadFile(source, path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		result[path] = &fstest.MapFile{Data: body}
	}
	return result
}
