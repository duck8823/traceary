package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestDoctorWarnsOnTerminalProjectionRows(t *testing.T) {
	t.Parallel()
	got := searchProjectionTerminalRowsDoctorCheck(apptypes.SearchProjectionStatus{
		TerminalGenerations:     2,
		TerminalKeywordRows:     10,
		TerminalFingerprintRows: 4,
	}, nil)
	if got.Name != "search-projection-terminal-rows" {
		t.Fatalf("name=%q", got.Name)
	}
	if got.Status != doctorStatusWarn {
		t.Fatalf("status=%q, want warn", got.Status)
	}
	if got.Hint != apptypes.SearchProjectionRecoveryCommand {
		t.Fatalf("hint=%q", got.Hint)
	}
	if !strings.Contains(got.Message, "2 terminal") || !strings.Contains(got.Message, "10 keyword") {
		t.Fatalf("message=%q", got.Message)
	}
	pass := searchProjectionTerminalRowsDoctorCheck(apptypes.SearchProjectionStatus{}, nil)
	if pass.Status != doctorStatusPass {
		t.Fatalf("empty status=%q, want pass", pass.Status)
	}
}

func TestDoctorFixNamesTerminalReclaimAction(t *testing.T) {
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
	log, recorded := root.applySearchProjectionTerminalReclaim(ctx, doctorCommandInput{})
	if !recorded {
		t.Fatalf("fix was not recorded, log=%+v gen=%s", log, started.GenerationID)
	}
	if log.Name != "search-projection-terminal-reclaim" {
		t.Fatalf("name=%q", log.Name)
	}
	if log.Error != "" {
		t.Fatalf("error=%q action=%q", log.Error, log.Action)
	}
	if !strings.Contains(log.Action, "reclaimed") {
		t.Fatalf("action=%q, want reclaimed", log.Action)
	}
}

func TestStoreCompactJSONIncludesProjectionReclaimStep(t *testing.T) {
	stub := &compactionStepStub{}
	root := NewRootCLI(WithStoreCompactionFactory(func(string) application.StoreCompactionUsecase { return stub })).Command()
	path := t.TempDir() + "/store.db"
	root.SetArgs([]string{"store", "compact", "--db-path", path})
	var stdout strings.Builder
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &payload); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", stdout.String(), err)
	}
	if _, ok := payload["released_command_body_rows"]; !ok {
		t.Fatal("released_command_body_rows missing")
	}
	steps, ok := payload["steps"].(map[string]any)
	if !ok {
		t.Fatalf("steps missing: %v", payload)
	}
	if _, ok := steps[application.CompactStepProjectionReclaim]; !ok {
		t.Fatalf("steps=%v, want projection_reclaim", steps)
	}
}

type compactionStepStub struct {
	compactionCLIStub
}

func (s *compactionStepStub) Compact(_ context.Context, in application.CompactInput) (application.CompactResult, error) {
	s.compacted = in.Source
	return application.CompactResult{
		Run: domain.CompactionRun{ID: "run", Phase: domain.CompactionCommitted, RollbackPath: in.Source + ".rollback-run"},
		Steps: application.CompactSteps{{
			Name:   application.CompactStepProjectionReclaim,
			Rows:   3,
			Detail: map[string]int64{"generations": 1, "keyword_rows": 2, "fingerprint_rows": 1},
		}},
	}, nil
}
