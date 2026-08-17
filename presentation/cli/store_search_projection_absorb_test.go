package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestStoreCompactProjectionRebuildStartsGeneration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	database := infra.NewDatabase(path, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	root := NewRootCLI(WithSearchProjection(usecase.NewSearchProjectionUsecase(database))).Command()
	root.SetArgs([]string{"store", "compact", "--projection-rebuild"})
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["generation_id"] == nil || payload["generation_id"] == "" {
		t.Fatalf("generation_id missing: %s", stdout.String())
	}

	stdout.Reset()
	root = NewRootCLI(WithSearchProjection(usecase.NewSearchProjectionUsecase(database))).Command()
	root.SetArgs([]string{"store", "compact", "--projection-abort"})
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("abort stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["state"] != "failed" && payload["state"] != "abandoned" {
		// Abandon parks as failed/abandoned depending on prior state.
		if got, _ := payload["failure_class"].(string); got != "abandoned" && payload["state"] != "failed" {
			t.Fatalf("abort payload=%s", stdout.String())
		}
	}
}

func TestStoreCompactProjectionFlagsRejectDBPath(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	root.SetArgs([]string{"store", "compact", "--projection-rebuild", "--db-path", "other.db"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected --db-path with --projection-rebuild to fail")
	}
	if !strings.Contains(err.Error(), "--db-path") {
		t.Fatalf("error=%q, want --db-path rejected", err.Error())
	}
}

func TestStoreCompactProjectionBudgetFlagsRequireRebuild(t *testing.T) {
	t.Parallel()
	root := NewRootCLI().Command()
	root.SetArgs([]string{"store", "compact", "--index-family-bytes", "1024"})
	var stderr strings.Builder
	root.SetErr(&stderr)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected budget flags without --projection-rebuild to fail")
	}
	if !strings.Contains(err.Error(), "--projection-rebuild") {
		t.Fatalf("error=%q, want --projection-rebuild required", err.Error())
	}
}
