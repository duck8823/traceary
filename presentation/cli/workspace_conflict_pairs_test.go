package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_WorkspaceIdentityReportCountsConflictPairs(t *testing.T) {
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

	stdout := &bytes.Buffer{}
	root := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithWorkspaceIdentity(identityUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"report", "workspace-identity", "--json", "--db-path", dbPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stdout.String())
	}

	var envelope struct {
		Workspace struct {
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
		} `json:"workspace_identity"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("JSON = %s, unmarshal error = %v", stdout.String(), err)
	}
	if envelope.Workspace.ConflictPairCount != 2 {
		t.Fatalf("conflict_pair_count = %d, want 2", envelope.Workspace.ConflictPairCount)
	}
	if len(envelope.Workspace.Sources) != 1 || envelope.Workspace.Sources[0].Relationships.Conflict != 4 || envelope.Workspace.Sources[0].ConflictPairCount != 2 {
		t.Fatalf("sources = %#v", envelope.Workspace.Sources)
	}
	got := map[string]bool{}
	for _, sample := range envelope.Workspace.ConflictSamples {
		got[sample.Workspace] = true
		if sample.SessionID != "session-1" {
			t.Fatalf("sample session = %q", sample.SessionID)
		}
	}
	if !got["/other"] || !got["/third"] || len(envelope.Workspace.ConflictSamples) != 2 {
		t.Fatalf("samples = %#v", envelope.Workspace.ConflictSamples)
	}

	textOut := &bytes.Buffer{}
	textCmd := newTestRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithWorkspaceIdentity(identityUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	textCmd.SetOut(textOut)
	textCmd.SetErr(&bytes.Buffer{})
	textCmd.SetArgs([]string{"report", "workspace-identity", "--db-path", dbPath})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("text Execute() error = %v\n%s", err, textOut.String())
	}
	for _, want := range []string{"conflict_pairs=2", "workspace=/other", "workspace=/third"} {
		if !bytes.Contains(textOut.Bytes(), []byte(want)) {
			t.Fatalf("text output missing %q:\n%s", want, textOut.String())
		}
	}
}
