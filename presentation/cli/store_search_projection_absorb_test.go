package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
	_ "modernc.org/sqlite"
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
	if payload["result_kind"] != apptypes.SearchProjectionResultKindGeneration {
		t.Fatalf("result_kind=%v, want %q", payload["result_kind"], apptypes.SearchProjectionResultKindGeneration)
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

func TestApplySearchProjectionRecoverySkipsHealthyInFlightRebuild(t *testing.T) {
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
	if _, err := projection.StartGeneration(ctx, apptypes.DefaultSearchProjectionBudget(), time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := projection.ControlStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != "rebuilding" && before.GenerationID == "" {
		t.Fatalf("setup state=%q gen=%q", before.State, before.GenerationID)
	}

	root := NewRootCLI(WithSearchProjection(projection))
	log, recorded := root.applySearchProjectionRecovery(ctx, doctorCommandInput{})
	if recorded {
		t.Fatalf("doctor --fix recorded healthy rebuild as parked recovery: %+v", log)
	}
	after, err := projection.ControlStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.GenerationID != before.GenerationID {
		t.Fatalf("generation changed: before=%s after=%s", before.GenerationID, after.GenerationID)
	}
}

func TestApplySearchProjectionRecoveryStartsParkedFailedGeneration(t *testing.T) {
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
	started, err := projection.StartGeneration(ctx, apptypes.DefaultSearchProjectionBudget(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.Abandon(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}

	root := NewRootCLI(WithSearchProjection(projection))
	log, recorded := root.applySearchProjectionRecovery(ctx, doctorCommandInput{})
	if !recorded {
		t.Fatal("doctor --fix must record parked failed recovery")
	}
	if log.Error != "" {
		t.Fatalf("recovery error=%q action=%q", log.Error, log.Action)
	}
	if log.Action == "" {
		t.Fatalf("expected recovery action, log=%+v", log)
	}
	after, err := projection.ControlStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.GenerationID == "" || after.GenerationID == started.GenerationID {
		t.Fatalf("failed generation must be replaced: parked=%s after=%s", started.GenerationID, after.GenerationID)
	}
}

func TestStoreCompactProjectionRebuildResumesDriftedCleanupWhenHashMatches(t *testing.T) {
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
	started, err := projection.StartGeneration(ctx, apptypes.DefaultSearchProjectionBudget(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`UPDATE search_projection_state SET state='drifted', phase='cleanup' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	root := NewRootCLI(WithSearchProjection(usecase.NewSearchProjectionUsecase(database))).Command()
	root.SetArgs([]string{"store", "compact", "--projection-rebuild"})
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("rebuild drifted/cleanup: %v stdout=%s", err, stdout.String())
	}
	after, err := projection.ControlStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.GenerationID != started.GenerationID {
		t.Fatalf("matching drifted/cleanup must resume, not replace: started=%s after=%s stdout=%s", started.GenerationID, after.GenerationID, stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if payload["result_kind"] != apptypes.SearchProjectionResultKindRun {
		t.Fatalf("result_kind=%v, want %q stdout=%s", payload["result_kind"], apptypes.SearchProjectionResultKindRun, stdout.String())
	}
	if payload["batches"] == nil || payload["stop_reason"] == nil {
		t.Fatalf("run-result fields missing: %s", stdout.String())
	}
}

func TestStoreCompactProjectionRebuildJSONDiscriminator(t *testing.T) {
	ctx := context.Background()
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("fresh start is generation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.db")
		database := infra.NewDatabase(path, migrations)
		if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
			t.Fatal(err)
		}
		payload := executeProjectionRebuild(t, database, []string{"store", "compact", "--projection-rebuild"})
		if payload["result_kind"] != apptypes.SearchProjectionResultKindGeneration {
			t.Fatalf("payload=%v", payload)
		}
		if payload["generation_id"] == nil || payload["generation_id"] == "" {
			t.Fatalf("generation_id missing: %v", payload)
		}
		if _, hasBatches := payload["batches"]; hasBatches {
			t.Fatalf("generation shape must not require sniffing batches: %v", payload)
		}
	})

	t.Run("replace after hash mismatch is generation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.db")
		database := infra.NewDatabase(path, migrations)
		if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
			t.Fatal(err)
		}
		first := executeProjectionRebuild(t, database, []string{"store", "compact", "--projection-rebuild"})
		firstID, _ := first["generation_id"].(string)
		if first["result_kind"] != apptypes.SearchProjectionResultKindGeneration || firstID == "" {
			t.Fatalf("first=%v", first)
		}
		second := executeProjectionRebuild(t, database, []string{"store", "compact", "--projection-rebuild", "--index-family-bytes", "1048576"})
		secondID, _ := second["generation_id"].(string)
		if second["result_kind"] != apptypes.SearchProjectionResultKindGeneration {
			t.Fatalf("second=%v", second)
		}
		if secondID == "" || secondID == firstID {
			t.Fatalf("mismatch must replace: first=%s second=%s payload=%v", firstID, secondID, second)
		}
	})

	t.Run("hash-match resume is run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.db")
		database := infra.NewDatabase(path, migrations)
		if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := usecase.NewSearchProjectionUsecase(database).StartGeneration(ctx, apptypes.DefaultSearchProjectionBudget(), time.Now()); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if _, err = db.Exec(`UPDATE search_projection_state SET state='drifted', phase='cleanup' WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		payload := executeProjectionRebuild(t, database, []string{"store", "compact", "--projection-rebuild"})
		if payload["result_kind"] != apptypes.SearchProjectionResultKindRun {
			t.Fatalf("payload=%v", payload)
		}
		if payload["batches"] == nil || payload["progress"] == nil || payload["stop_reason"] == nil {
			t.Fatalf("run-result fields missing: %v", payload)
		}
	})
}

func executeProjectionRebuild(t *testing.T, database *infra.Database, args []string) map[string]any {
	t.Helper()
	root := NewRootCLI(WithSearchProjection(usecase.NewSearchProjectionUsecase(database))).Command()
	root.SetArgs(args)
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(%v) error=%v stdout=%s", args, err, stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	return payload
}
