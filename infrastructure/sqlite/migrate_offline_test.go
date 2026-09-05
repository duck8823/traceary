package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestImplicitOpenRefusesOfflineMigrationsOnPopulatedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 35))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	insertSourceEvent(t, path, "legacy-event")

	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	err := current.Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	if diff := cmp.Diff([]int64{35, 45, 76, 78, 79, 80, 81, 82, 83}, required.Versions); diff != "" {
		t.Fatalf("pending offline (-want +got):\n%s", diff)
	}
	if got := maxSchemaVersion(t, path); got != 34 {
		t.Fatalf("schema version=%d, want 34 (store unmodified)", got)
	}
}

func TestStoreInitAppliesOfflineMigrations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 35))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	insertSourceEvent(t, path, "legacy-event")

	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	if err := current.InitializeAuthorized(ctx); err != nil {
		t.Fatal(err)
	}
	if got := maxSchemaVersion(t, path); got != 83 {
		t.Fatalf("schema version=%d, want 83", got)
	}
}

func TestEmptyStoreReachesCurrentSchemaImplicitly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	store := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if got := maxSchemaVersion(t, path); got != 83 {
		t.Fatalf("schema version=%d, want 83", got)
	}
}

func TestReclassifiedMigrationsApplyImplicitlyUntilNextOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 41))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	insertSourceEvent(t, path, "after-40")

	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	err := current.Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	if diff := cmp.Diff([]int64{45, 76, 78, 79, 80, 81, 82, 83}, required.Versions); diff != "" {
		t.Fatalf("pending offline (-want +got):\n%s", diff)
	}
	if got := maxSchemaVersion(t, path); got != 44 {
		t.Fatalf("schema version=%d, want 44 after implicit 41-44", got)
	}
}

func TestImplicitOpenAfterInterruptedOfflineDoesNotRerunIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	partial := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 45))
	if err := partial.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	insertSourceEvent(t, path, "waiting-on-45")

	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	started := time.Now()
	err := current.Initialize(ctx)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("implicit refuse took %s, want immediate", elapsed)
	}
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	if diff := cmp.Diff([]int64{45, 76, 78, 79, 80, 81, 82, 83}, required.Versions); diff != "" {
		t.Fatalf("pending offline (-want +got):\n%s", diff)
	}
	if got := maxSchemaVersion(t, path); got != 44 {
		t.Fatalf("schema version=%d, want last committed 44", got)
	}
}

func TestOfflineMigrationNeverRunsAtOpenEvenWithoutEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 35))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	insertMemoryOnly(t, path, "mem-1")
	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	err := current.Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	if got := maxSchemaVersion(t, path); got != 34 {
		t.Fatalf("schema version=%d, want 34 (store unmodified)", got)
	}
}

func TestOfflineMigrationNeverRunsAtOpenOnEmptyStore(t *testing.T) {
	t.Skip("WITHHELD-PENDING-OWNER-DECISION #2328: empty-store bootstrap (options A/B/C) is undecided")
}

func TestNormalInitializePerformsNoUpgradeWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 35))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	insertSourceEvent(t, path, "legacy-event")
	before := fileDigestForTest(t, path)
	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	err := current.Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	after := fileDigestForTest(t, path)
	if before != after {
		t.Fatal("Initialize mutated the live store")
	}
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.Contains(name, ".upgrade-") || strings.Contains(name, ".rollback-") || strings.Contains(name, ".traceary.compact-pending") || strings.HasSuffix(name, ".jsonl") {
			t.Fatalf("unexpected upgrade artifact %s", name)
		}
	}
}

func insertMemoryOnly(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`INSERT INTO memories(id,type,scope_kind,scope_value,fact,status,confidence,source,created_at,updated_at) VALUES(?,'decision','global','','fact','accepted','high','user',?,?)`, id, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
}

func fileDigestForTest(t *testing.T, path string) string {
	t.Helper()
	sum, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(sum)
}

func insertSourceEvent(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace) VALUES(?,'note','a','s','body',?,'c','w')`, id, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
}

func maxSchemaVersion(t *testing.T, path string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int64
	if err = db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestPreviewOfflineMigrationsNamesPendingVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	legacy := newStoreManagementDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 35))
	if err := legacy.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	got, err := current.PreviewOfflineMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]int64{35, 45, 76, 78, 79, 80, 81, 82, 83}, got); diff != "" {
		t.Fatalf("preview (-want +got):\n%s", diff)
	}
}
