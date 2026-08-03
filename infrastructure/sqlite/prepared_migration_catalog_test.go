package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
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
	defer db.Close()
	plan, err := BuildPreparedMigrationPlan(ctx, db, preparedMigrations(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Current != 34 || plan.Latest != 43 || len(plan.Pending) != 9 || !plan.Offline || len(plan.Digest) != 64 {
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
	defer db.Close()
	if _, err = BuildPreparedMigrationPlan(ctx, db, changed); err == nil {
		t.Fatal("plan accepted a migration body that does not match the reviewed manifest")
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
