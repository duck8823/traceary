package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestStoreOpen_IssuesNoProjectionWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	store := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := sql.Open("sqlite", sqliteDSN(path)); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace)
VALUES('evt-open','note','codex','sess-open','body',?,'cli','ws')`, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		_ = raw.Close()
		t.Fatalf("seed event: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	log := &repairStatementLog{}
	installRepairStatementObserver(t, path, log)
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).Initialize(ctx); err != nil {
		t.Fatalf("second Initialize() error = %v", err)
	}
	for _, query := range log.queries {
		if touchesDroppedSearchFamily(query) {
			t.Fatalf("second open touched dropped search family:\n%s", query)
		}
	}
}

func TestStoreOpen_NonEventStoreDoesNotApplyOfflineDrop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 80))).Initialize(ctx); err != nil {
		t.Fatalf("seed v79 Initialize() error = %v", err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := raw.Exec(`INSERT INTO memories(id,type,scope_kind,scope_value,fact,status,confidence,source,created_at,updated_at) VALUES('mem-1','decision','global','','fact','accepted','high','user',?,?)`, now, now); err != nil {
		_ = raw.Close()
		t.Fatalf("seed memory: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	err = NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("Initialize() error = %v, want OfflineMigrationsRequiredError", err)
	}
	if len(required.Versions) == 0 || required.Versions[len(required.Versions)-1] != 83 {
		t.Fatalf("pending versions = %v, want suffix 83", required.Versions)
	}
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var present int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='search_projection_state'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 1 {
		t.Fatalf("live open applied v80 DROP; search_projection_state count=%d", present)
	}
}

func TestNoProbeForRemovedTables(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]string{
		"infrastructure/sqlite/drop_search_projection_family.go":            "Layer-2 verifier table literals",
		"infrastructure/sqlite/prepared_migration_catalog.go":               "historical catalog filenames and comments",
		"infrastructure/sqlite/prepared_upgrade_verifier.go":                "named verifier id switch",
		"infrastructure/sqlite/legacy_search_retire.go":                     "migration-032 event_search_projection view drop",
		"schema/sqlite/migrations/000080_drop_search_projection_family.sql": "forward DROP migration",
	}
	names := append([]string(nil), droppedSearchFamilyTables...)
	var hits []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", path, walkErr)
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "testdata" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relative path for %s: %w", path, relErr)
		}
		if strings.HasPrefix(rel, "schema/sqlite/migrations/") && rel != "schema/sqlite/migrations/000080_drop_search_projection_family.sql" {
			return nil
		}
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		ext := filepath.Ext(rel)
		isGo := ext == ".go"
		isLiveSQL := strings.HasPrefix(rel, "infrastructure/sqlite/sql/") && ext == ".sql"
		isV80 := rel == "schema/sqlite/migrations/000080_drop_search_projection_family.sql"
		if !isGo && !isLiveSQL && !isV80 {
			return nil
		}
		if _, ok := allowed[rel]; ok {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", rel, readErr)
		}
		text := string(body)
		for _, name := range names {
			if strings.Contains(text, name) {
				hits = append(hits, rel+": "+name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("non-test source still names dropped search family objects:\n%s", strings.Join(hits, "\n"))
	}
}

func touchesDroppedSearchFamily(query string) bool {
	lower := strings.ToLower(query)
	for _, name := range droppedSearchFamilyTables {
		if strings.Contains(lower, name) {
			return true
		}
	}
	return false
}
