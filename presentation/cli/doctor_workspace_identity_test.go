package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

type doctorWorkspaceIdentityJSON struct {
	Coverage struct {
		EventCount int `json:"event_count"`
	} `json:"coverage"`
	ConflictPairCount int `json:"conflict_pair_count"`
	Sources           []struct {
		Relationships struct {
			Conflict int `json:"conflict"`
		} `json:"relationships"`
		ConflictPairCount int `json:"conflict_pair_count"`
	} `json:"sources"`
	ConflictSamples []struct {
		Workspace string `json:"workspace"`
		SessionID string `json:"session_id"`
	} `json:"conflict_samples"`
	Aliases       []struct{} `json:"aliases"`
	ExactDelivery struct {
		AttemptCount         int     `json:"attempt_count"`
		ExactRedeliveryCount int     `json:"exact_redelivery_count"`
		ExactRedeliveryRate  float64 `json:"exact_redelivery_rate"`
		SampleAvailable      bool    `json:"sample_available"`
		TargetMet            bool    `json:"target_met"`
	} `json:"exact_delivery"`
}

func TestRootCLI_DoctorJSONIncludesWorkspaceIdentity(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	identity := &workspaceIdentityUsecaseStub{report: apptypes.WorkspaceIdentityReport{
		Coverage: apptypes.WorkspaceIdentityCoverage{EventCount: 2, CoveredEvents: 2, CoverageRate: 1},
		Sources: []apptypes.WorkspaceIdentitySourceReport{{
			Client: "codex", SourceHook: "user_prompt_submit",
			DeliveryAttemptCount: 200, RuntimeAttemptCount: 200, ExactRedeliveryCount: 1,
		}},
	}}
	root := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithWorkspaceIdentity(identity),
	).Command()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	root.SetArgs([]string{"doctor", "--db-path", dbPath, "--json", "--warnings-ok", "--client", "codex", "--project-dir", t.TempDir()})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stdout.String())
	}
	var report struct {
		WorkspaceIdentity doctorWorkspaceIdentityJSON `json:"workspace_identity"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("JSON = %s, unmarshal error = %v", stdout.String(), err)
	}
	if report.WorkspaceIdentity.ExactDelivery.AttemptCount != 200 || report.WorkspaceIdentity.ExactDelivery.ExactRedeliveryCount != 1 {
		t.Fatalf("exact_delivery = %+v", report.WorkspaceIdentity.ExactDelivery)
	}
	if !report.WorkspaceIdentity.ExactDelivery.SampleAvailable || !report.WorkspaceIdentity.ExactDelivery.TargetMet {
		t.Fatalf("exact_delivery = %+v", report.WorkspaceIdentity.ExactDelivery)
	}
	if strings.Contains(stdout.String(), "heuristic_candidates") {
		t.Fatalf("doctor --json must not include retired heuristic_candidates:\n%s", stdout.String())
	}
	if identity.limit != 3 {
		t.Fatalf("Report limit = %d, want 3", identity.limit)
	}
}

func TestRootCLI_DoctorJSONWorkspaceIdentityIsByteStable(t *testing.T) {
	dbPath, identityUC, storeUC, setter := seededWorkspaceIdentityStore(t)
	args := []string{"doctor", "--db-path", dbPath, "--json", "--warnings-ok", "--client", "codex", "--project-dir", t.TempDir()}
	first := executeDoctorJSONBytes(t, storeUC, identityUC, setter, args)
	second := executeDoctorJSONBytes(t, storeUC, identityUC, setter, args)
	if !bytes.Equal(first, second) {
		t.Fatalf("doctor --json not byte-stable\nfirst=%s\nsecond=%s", first, second)
	}
	var report struct {
		WorkspaceIdentity doctorWorkspaceIdentityJSON `json:"workspace_identity"`
	}
	if err := json.Unmarshal(first, &report); err != nil {
		t.Fatalf("JSON = %s, unmarshal error = %v", first, err)
	}
	if report.WorkspaceIdentity.ConflictPairCount != 2 {
		t.Fatalf("conflict_pair_count = %d, want 2", report.WorkspaceIdentity.ConflictPairCount)
	}
	if report.WorkspaceIdentity.Sources == nil {
		t.Fatal("sources is null")
	}
}

func TestRootCLI_WorkspaceIdentityReportCountsConflictPairsOnDoctorJSON(t *testing.T) {
	dbPath, identityUC, storeUC, setter := seededWorkspaceIdentityStore(t)
	stdout := executeDoctorJSONBytes(t, storeUC, identityUC, setter, []string{
		"doctor", "--db-path", dbPath, "--json", "--warnings-ok", "--client", "codex", "--project-dir", t.TempDir(),
	})
	var report struct {
		WorkspaceIdentity doctorWorkspaceIdentityJSON `json:"workspace_identity"`
		Checks            []struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("JSON = %s, unmarshal error = %v", stdout, err)
	}
	identity := report.WorkspaceIdentity
	if identity.ConflictPairCount != 2 {
		t.Fatalf("conflict_pair_count = %d, want 2", identity.ConflictPairCount)
	}
	if len(identity.Sources) != 1 || identity.Sources[0].Relationships.Conflict != 4 || identity.Sources[0].ConflictPairCount != 2 {
		t.Fatalf("sources = %#v", identity.Sources)
	}
	got := map[string]bool{}
	for _, sample := range identity.ConflictSamples {
		got[sample.Workspace] = true
		if sample.SessionID != "session-1" {
			t.Fatalf("sample session = %q", sample.SessionID)
		}
	}
	if !got["/other"] || !got["/third"] || len(identity.ConflictSamples) != 2 {
		t.Fatalf("samples = %#v", identity.ConflictSamples)
	}
	found := false
	for _, check := range report.Checks {
		if check.Name == "workspace-aliases" && strings.Contains(check.Message, "conflict pairs=2") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("workspace-aliases check missing conflict pairs: %s", stdout)
	}
}

func TestRootCLI_ReportStillRunsAfterWorkspaceIdentityRemoval(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	root := cli.NewRootCLI(cli.WithStoreManagement(&storeManagementUsecaseStub{})).Command()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"report", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("report --help error = %v", err)
	}
}

func executeDoctorJSONBytes(t *testing.T, storeUC usecase.StoreManagementUsecase, identityUC usecase.WorkspaceIdentityUsecase, setter func(string), args []string) []byte {
	t.Helper()
	stdout := &bytes.Buffer{}
	root := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithWorkspaceIdentity(identityUC),
		cli.WithDatabasePathSetter(setter),
	).Command()
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v\n%s", args, err, stdout.String())
	}
	return stdout.Bytes()
}

func seededWorkspaceIdentityStore(t *testing.T) (string, usecase.WorkspaceIdentityUsecase, usecase.StoreManagementUsecase, func(string)) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	identityDS := sqliteinfra.NewWorkspaceIdentityDatasource(db)
	identityUC := usecase.NewWorkspaceIdentityUsecase(identityDS, identityDS, nil)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO sessions (session_id, started_at, client, agent, workspace) VALUES ('session-1', '2026-08-15T00:00:00Z', 'hook', 'codex', '/repo')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close session insert: %v", err)
	}

	for i, item := range []struct {
		id, workspace, native string
	}{
		{"event-a1", "/other", "delivery-a1"},
		{"event-a2", "/other", "delivery-a2"},
		{"event-a3", "/other", "delivery-a3"},
		{"event-b1", "/third", "delivery-b1"},
	} {
		event := model.EventOfWithSourceHook(
			types.EventID(item.id),
			types.EventKindPrompt,
			types.Client("hook"),
			types.Agent("codex"),
			types.SessionID("session-1"),
			types.Workspace(item.workspace),
			item.id,
			time.Date(2026, 8, 15, 0, i, 0, 0, time.UTC),
			"post_tool_use",
		)
		event.SetRawWorkspace(item.workspace)
		evidence, err := model.NewHookDeliveryEvidence(event, item.native, item.workspace)
		if err != nil {
			t.Fatalf("NewHookDeliveryEvidence(%s) error = %v", item.id, err)
		}
		event.SetDeliveryEvidence(evidence)
		if err := eventDS.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", item.id, err)
		}
	}
	return dbPath, identityUC, storeUC, db.SetPath
}
