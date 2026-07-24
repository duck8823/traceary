package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
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
	insertSession := func(sessionID string) {
		t.Helper()
		if _, execErr := raw.Exec(
			`INSERT INTO sessions (session_id, started_at, ended_at, client, agent, workspace, runtime_mode, terminal_reason)
			 VALUES (?, ?, ?, 'codex', 'codex', 'github.com/duck8823/traceary', 'one_shot', 'success')`,
			sessionID, from.Add(-time.Hour).Format(time.RFC3339Nano), from.Format(time.RFC3339Nano),
		); execErr != nil {
			t.Fatalf("insert session %s: %v", sessionID, execErr)
		}
	}
	for index := 0; index < 501; index++ {
		insertEvent(fmt.Sprintf("prompt-%03d", index), "prompt", "active-session", "user_prompt_submit", from.Add(time.Duration(index)*time.Second))
	}
	insertEvent("stop-covered", "transcript", "covered-session", "stop", from.Add(time.Hour))
	insertEvent("stop-missing", "transcript", "missing-session", "stop", from.Add(2*time.Hour))
	insertSession("missing-session")

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
	if got.StoredEvents != 503 || !got.PromptObserved || got.PromptTurns != 501 ||
		got.StopSessions != 2 || got.UncoveredFinalTurns != 501 ||
		got.StopSessionsWithUsage != 1 || got.UsageObservations != 2 || got.HeadlessUsageObservations != 1 ||
		got.UsageKnown != 0 || got.UsageUnavailable != 2 {
		t.Fatalf("evidence = %+v", got)
	}
}

func TestCodexCaptureDiagnosticDatasource_CorrelatesHookPromptsBySessionAndStableOrder(t *testing.T) {
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

	from := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	insert := func(id, kind, sessionID, workspace, hook string, at time.Time) {
		t.Helper()
		if _, err := raw.Exec(
			`INSERT INTO events (id, kind, agent, session_id, body, created_at, client, workspace, source_hook)
			 VALUES (?, ?, 'codex', ?, 'private body', ?, 'hook', ?, ?)`,
			id, kind, sessionID, at.Format(time.RFC3339Nano), workspace, hook,
		); err != nil {
			t.Fatalf("insert event %q: %v", id, err)
		}
	}
	workspace := "github.com/duck8823/traceary"
	// A Stop before the first prompt must not be backfilled onto that prompt.
	insert("s-before", "transcript", "same", workspace, "stop", from.Add(time.Second))
	insert("p-first", "prompt", "same", workspace, "user_prompt_submit", from.Add(2*time.Second))
	// The next prompt closes the first prompt's interval; only the second is covered.
	insert("p-second", "prompt", "same", workspace, "user_prompt_submit", from.Add(10*time.Second))
	insert("s-second", "transcript", "same", workspace, "stop", from.Add(11*time.Second))
	// Non-hook prompts do not split the hook prompt's turn interval.
	insert("p-third", "prompt", "same", workspace, "user_prompt_submit", from.Add(20*time.Second))
	insert("non-hook", "prompt", "same", workspace, "", from.Add(21*time.Second))
	insert("s-third", "transcript", "same", workspace, "stop", from.Add(22*time.Second))
	// Equal timestamps use ID as a stable order: p-tied < s-tied < p-last.
	tied := from.Add(30 * time.Second)
	insert("p-tied", "prompt", "same", workspace, "user_prompt_submit", tied)
	insert("s-tied", "transcript", "same", workspace, "stop", tied)
	// The final prompt is deliberately uncovered; a Stop in another session cannot cover it.
	insert("p-last", "prompt", "same", workspace, "user_prompt_submit", tied)
	insert("s-other", "transcript", "other", workspace, "stop", from.Add(31*time.Second))

	criteria, err := apptypes.CodexCaptureDiagnosticCriteriaOf(types.Workspace(workspace), from, to)
	if err != nil {
		t.Fatalf("criteria: %v", err)
	}
	got, err := sqlite.NewCodexCaptureDiagnosticDatasource(database).LoadCodexCaptureDiagnostic(ctx, criteria)
	if err != nil {
		t.Fatalf("LoadCodexCaptureDiagnostic: %v", err)
	}
	if got.PromptTurns != 5 || got.UncoveredFinalTurns != 2 {
		t.Fatalf("prompt correlation = %+v, want 5 prompt turns with first and last uncovered", got)
	}
}

func TestCodexCaptureDiagnosticDatasource_ScopesFinalizedHeadlessUsageToCanonicalSessionWorkspaceAndObservationWindow(t *testing.T) {
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

	from := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	insertSession := func(sessionID, workspace string) {
		t.Helper()
		if _, err := raw.Exec(
			`INSERT INTO sessions (session_id, started_at, ended_at, client, agent, workspace, runtime_mode, terminal_reason)
			 VALUES (?, ?, ?, 'codex', 'codex', ?, 'one_shot', 'success')`,
			sessionID, from.Add(-time.Hour).Format(time.RFC3339Nano), from.Format(time.RFC3339Nano), workspace,
		); err != nil {
			t.Fatalf("insert session: %v", err)
		}
	}
	insertUsage := func(id, sessionID string, observedAt time.Time) {
		t.Helper()
		if _, err := raw.Exec(
			`INSERT INTO usage_observations (
			 observation_id, session_id, host, source_name, source_version, provider, model,
			 scope, accounting, status, observed_at, finalized_at, terminal_code,
			 input_state, cached_input_state, cache_write_input_state, output_state, reasoning_output_state,
			 total_state, cost_state
			) VALUES (?, ?, 'codex', 'headless_stream', 'schema-v1', 'openai', NULL,
			 'call', 'excluded', 'finalized', ?, ?, 'success',
			 'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable',
			 'unavailable', 'unavailable')`,
			id, sessionID, observedAt.Format(time.RFC3339Nano), observedAt.Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("insert usage: %v", err)
		}
	}
	const targetWorkspace = "github.com/duck8823/traceary"
	// No events are inserted: finalized headless-only sessions must still be
	// visible through the canonical sessions ownership projection.
	insertSession("target-session", targetWorkspace)
	insertSession("other-session", "github.com/duck8823/other")
	insertUsage("headless-target", "target-session", from.Add(2*time.Second))
	insertUsage("headless-other-workspace", "other-session", from.Add(2*time.Second))
	insertUsage("headless-outside-window", "target-session", to.Add(time.Second))
	var targetSessionCount, targetUsageCount int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sessions WHERE agent = 'codex' AND workspace = ?`, targetWorkspace).Scan(&targetSessionCount); err != nil {
		t.Fatalf("count target sessions: %v", err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM usage_observations WHERE session_id = 'target-session' AND source_name = 'headless_stream'`).Scan(&targetUsageCount); err != nil {
		t.Fatalf("count target usage: %v", err)
	}
	if targetSessionCount != 1 || targetUsageCount != 2 {
		t.Fatalf("fixture setup target sessions=%d usage=%d", targetSessionCount, targetUsageCount)
	}
	criteria, err := apptypes.CodexCaptureDiagnosticCriteriaOf(targetWorkspace, from, to)
	if err != nil {
		t.Fatalf("criteria: %v", err)
	}
	got, err := sqlite.NewCodexCaptureDiagnosticDatasource(database).LoadCodexCaptureDiagnostic(ctx, criteria)
	if err != nil {
		t.Fatalf("LoadCodexCaptureDiagnostic: %v", err)
	}
	if got.StoredEvents != 0 || got.HeadlessUsageObservations != 1 || got.UsageObservations != 1 || got.UsageUnavailable != 1 {
		t.Fatalf("headless workspace projection = %+v, want only target finalized in-window observation without events", got)
	}
}
