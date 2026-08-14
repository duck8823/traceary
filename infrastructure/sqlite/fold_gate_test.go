package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestFoldGateInspector_MeasuresWorthFoldingAndWakePerHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	refinements := sqlite.NewSessionRefinementDatasource(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t)))

	savePrompt := func(id, session, body string) {
		t.Helper()
		event := model.EventOf(
			types.EventID(id),
			types.EventKindPrompt,
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID(session),
			types.Workspace("ws"),
			body,
			time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		)
		if err := events.Save(ctx, event); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}
	savePrompt("small-1", "small", "tiny")
	savePrompt("large-1", "large", strings.Repeat("L", int(application.DefaultFoldThresholdBytes)+8))
	savePrompt("folded-1", "folded", "short after fold")
	savePrompt("wake-1", "wake-claude", "wake source")
	savePrompt("wake-2", "wake-gemini", "wake source")

	conn := openCallSiteDB(t, dbPath)
	for _, row := range []struct {
		id, started, client string
	}{
		{"small", "2026-08-01T00:00:00Z", "cli"},
		{"large", "2026-08-01T00:01:00Z", "cli"},
		{"folded", "2026-08-01T00:02:00Z", "codex"},
		{"wake-claude", "2026-08-01T00:03:00Z", "claude"},
		{"wake-gemini", "2026-08-01T00:04:00Z", "gemini"},
	} {
		if _, err := conn.Exec(`INSERT INTO sessions(session_id, started_at, ended_at, client, agent, workspace)
VALUES (?, ?, '2026-08-02T00:00:00Z', ?, 'codex', 'ws')`, row.id, row.started, row.client); err != nil {
			t.Fatalf("insert session %s: %v", row.id, err)
		}
	}

	mustRefine := func(session, summary string, degraded bool) {
		t.Helper()
		row, err := model.NewSessionRefinement(
			types.SessionID(session), 1,
			types.EventID("folded-1"), types.EventID("folded-1"),
			summary, "", "test",
			time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			degraded,
		)
		if err != nil {
			t.Fatalf("NewSessionRefinement(%s) error = %v", session, err)
		}
		if _, err := refinements.SaveIfAdvances(ctx, row, 0); err != nil {
			t.Fatalf("SaveIfAdvances(%s) error = %v", session, err)
		}
	}
	mustRefine("folded", "Mechanical summary (degraded=1).\nThis recovers when events occurred.", true)
	mustRefine("wake-claude", "We needed a fold-gate measurement so the v0.34 rows can be evaluated after the ask shipped.", false)
	mustRefine("wake-gemini", strings.Repeat("G", 9000), false)

	report, err := sqlite.NewFoldGateInspector(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))).
		InspectFoldGate(ctx, application.DefaultFoldThresholdBytes, application.DefaultFoldWakeBudgetBytes)
	if err != nil {
		t.Fatalf("InspectFoldGate() error = %v", err)
	}
	if report.SchemaVersion != "traceary.fold_gate/v1" {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if report.WorthFoldingCount != 4 {
		t.Fatalf("worth_folding = %d, want 4 (large+folded+two wake; small excluded)", report.WorthFoldingCount)
	}
	if report.RefinementCount != 3 {
		t.Fatalf("refinements = %d, want 3", report.RefinementCount)
	}
	if report.RefinementGate != "miss" {
		t.Fatalf("refinement_gate = %q, want miss (3/4 < 0.95)", report.RefinementGate)
	}
	if report.AgentRefinementCount != 2 {
		t.Fatalf("agent refinements = %d, want 2", report.AgentRefinementCount)
	}

	byClient := map[string]struct {
		eligible int64
		status   string
	}{}
	for _, host := range report.Wake {
		byClient[host.Client] = struct {
			eligible int64
			status   string
		}{host.EligibleCount, host.Status}
	}
	if byClient["claude"].status != "injects" || byClient["claude"].eligible != 1 {
		t.Fatalf("claude wake = %+v", byClient["claude"])
	}
	if byClient["gemini"].status != "over_budget" {
		t.Fatalf("gemini wake = %+v", byClient["gemini"])
	}
	if report.WakeGate != "miss" {
		t.Fatalf("wake_gate = %q, want miss (gemini over budget)", report.WakeGate)
	}
	if report.Content.Sampled != 2 || report.Content.ContentProxyOK != 2 || report.Content.MechanicalTemplate != 0 {
		t.Fatalf("content = %+v", report.Content)
	}
	if report.Evidence.Status != "complete" {
		t.Fatalf("evidence = %+v", report.Evidence)
	}
}
