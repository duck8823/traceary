package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
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

func TestStoreCompactProjectionRebuildFollowsOperatorParkHint(t *testing.T) {
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
	projection := usecase.NewSearchProjectionUsecase(database)

	root := NewRootCLI(WithSearchProjection(projection)).Command()
	root.SetArgs([]string{"store", "compact", "--projection-rebuild", "--index-family-bytes", "104857600"})
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &first); err != nil {
		t.Fatalf("first stdout %q: %v", stdout.String(), err)
	}
	firstID, _ := first["generation_id"].(string)
	if firstID == "" {
		t.Fatalf("first generation_id missing: %s", stdout.String())
	}

	status, err := projection.ControlStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	notice := apptypes.SearchProjectionStatus{
		State:      status.State,
		Phase:      status.Phase,
		ConfigHash: status.ConfigHash,
		Origin:     status.Origin,
	}
	notice.ApplyParkedNotice(apptypes.DefaultSearchProjectionBudget().ConfigHash())
	if notice.RecoveryCommand != apptypes.SearchProjectionStartCommand {
		t.Fatalf("recovery=%q, want %q", notice.RecoveryCommand, apptypes.SearchProjectionStartCommand)
	}

	stdout.Reset()
	root = NewRootCLI(WithSearchProjection(projection)).Command()
	root.SetArgs([]string{"store", "compact", "--projection-rebuild"})
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("hint command failed: %v", err)
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &second); err != nil {
		t.Fatalf("second stdout %q: %v", stdout.String(), err)
	}
	secondID, _ := second["generation_id"].(string)
	if secondID == "" || secondID == firstID {
		t.Fatalf("rebuild must replace the operator generation: first=%s second=%s stdout=%s", firstID, secondID, stdout.String())
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
