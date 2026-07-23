package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestCodexCaptureDiagnosticDatasource_CorrelatesCompleteStopUsageWithoutBodies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture DB: %v", err)
	}
	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close fixture DB: %v", closeErr)
		}
	}()

	from := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	insertEvent := func(id, kind, sessionID, sourceHook string, at time.Time) {
		t.Helper()
		if _, execErr := raw.Exec(
			`INSERT INTO events (id, kind, agent, session_id, body, created_at, client, workspace, source_hook)
			 VALUES (?, ?, 'codex', ?, 'private body must not be read', ?, 'hook', 'github.com/duck8823/traceary', ?)`,
			id, kind, sessionID, at.Format(time.RFC3339Nano), sourceHook,
		); execErr != nil {
			t.Fatalf("insert event %s: %v", id, execErr)
		}
	}
	for index := 0; index < 501; index++ {
		insertEvent(fmt.Sprintf("prompt-%03d", index), "prompt", "active-session", "", from.Add(time.Duration(index)*time.Second))
	}
	insertEvent("stop-covered", "transcript", "covered-session", "stop", from.Add(time.Hour))
	insertEvent("stop-missing", "transcript", "missing-session", "stop", from.Add(2*time.Hour))

	insertUsage := func(id, sessionID, sourceName, observedAt, totalState string, total any) {
		t.Helper()
		if _, execErr := raw.Exec(
			`INSERT INTO usage_observations (
			    observation_id, session_id, host, source_name, source_version, provider, model,
			    scope, accounting, status, observed_at, finalized_at, terminal_code,
			    input_state, input_tokens, cached_input_state, cached_input_tokens,
			    cache_write_input_state, cache_write_input_tokens, output_state, output_tokens,
			    reasoning_output_state, reasoning_output_tokens, total_state, total_tokens,
			    cost_state
			 ) VALUES (
			    ?, ?, 'codex', ?, 'schema-v1', 'openai', NULL,
			    'call', 'excluded', 'finalized', ?, ?, 'success',
			    'unavailable', NULL, 'unavailable', NULL,
			    'unavailable', NULL, 'unavailable', NULL,
			    'unavailable', NULL, ?, ?, 'unavailable'
			 )`,
			id, sessionID, sourceName, observedAt, observedAt, totalState, total,
		); execErr != nil {
			t.Fatalf("insert usage %s: %v", id, execErr)
		}
	}
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	insertUsage("codex:stop_hook:covered", "covered-session", "stop_hook", epoch, "unavailable", nil)
	insertUsage("codex:headless:missing", "missing-session", "headless_stream", from.Add(time.Hour).Format(time.RFC3339Nano), "unavailable", nil)
	insertUsage("codex:other-session", "other-session", "rollout_jsonl", from.Add(time.Hour).Format(time.RFC3339Nano), "unavailable", nil)

	criteria, err := apptypes.CodexCaptureDiagnosticCriteriaOf(
		"github.com/duck8823/traceary", from, to,
	)
	if err != nil {
		t.Fatalf("CodexCaptureDiagnosticCriteriaOf() error = %v", err)
	}
	got, err := sqlite.NewCodexCaptureDiagnosticDatasource(database).
		LoadCodexCaptureDiagnostic(ctx, criteria)
	if err != nil {
		t.Fatalf("LoadCodexCaptureDiagnostic() error = %v", err)
	}
	if got.StoredEvents != 503 || !got.PromptObserved || got.StopSessions != 2 ||
		got.StopSessionsWithUsage != 1 || got.UsageObservations != 1 ||
		got.UsageKnown != 0 || got.UsageUnavailable != 1 {
		t.Fatalf("evidence = %+v", got)
	}
}
