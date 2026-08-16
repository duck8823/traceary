package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestRepairEpochZeroHookUsage_BackfillsFromSessionBoundary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	store := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	sessionTime := time.Date(2026, 8, 15, 18, 30, 0, 0, time.UTC)
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "grok:stop_hook:repaired",
		sessionID:     "session-repaired",
		host:          "grok",
		sourceName:    "stop_hook",
		sessionTime:   sessionTime,
		withEvent:     true,
	})
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "antigravity:stop_hook:repaired",
		sessionID:     "session-repaired",
		host:          "antigravity",
		sourceName:    "stop_hook",
		sessionTime:   sessionTime,
	})
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "kimi:session_end_hook:repaired",
		sessionID:     "session-repaired",
		host:          "kimi",
		sourceName:    "session_end_hook",
		sessionTime:   sessionTime,
	})
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "gemini:after_agent_hook:repaired",
		sessionID:     "session-repaired",
		host:          "gemini",
		sourceName:    "after_agent_hook",
		sessionTime:   sessionTime,
	})
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "grok:headless_stream:leave",
		sessionID:     "session-repaired",
		host:          "grok",
		sourceName:    "headless_stream",
		sessionTime:   sessionTime,
	})
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "grok:stop_hook:orphan",
		sessionID:     "session-missing",
		host:          "grok",
		sourceName:    "stop_hook",
	})

	result, err := sqlite.RepairEpochZeroHookUsageObservations(ctx, conn, 10)
	if err != nil {
		t.Fatalf("RepairEpochZeroHookUsageObservations() error = %v", err)
	}
	if result.Repaired != 4 || result.Skipped != 1 || result.MorePending {
		t.Fatalf("repair result = %+v, want repaired=4 skipped=1", result)
	}

	want := sessionTime.UTC().Format(time.RFC3339Nano)
	for _, id := range []string{
		"grok:stop_hook:repaired",
		"antigravity:stop_hook:repaired",
		"kimi:session_end_hook:repaired",
		"gemini:after_agent_hook:repaired",
	} {
		assertUsageTimes(t, conn, id, want, want)
	}
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	assertUsageTimes(t, conn, "grok:headless_stream:leave", epoch, epoch)
	assertUsageTimes(t, conn, "grok:stop_hook:orphan", epoch, epoch)

	rerun, err := sqlite.RepairEpochZeroHookUsageObservations(ctx, conn, 10)
	if err != nil {
		t.Fatalf("rerun error = %v", err)
	}
	if rerun.Repaired != 0 || rerun.Skipped != 1 || rerun.Selected != 1 {
		t.Fatalf("rerun = %+v, want only the unrepairable orphan", rerun)
	}

	_, err = conn.Exec(`UPDATE usage_observations SET observed_at = ? WHERE observation_id = ?`,
		time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), "grok:stop_hook:repaired")
	if err == nil {
		t.Fatal("descriptor trigger did not reject observed_at update after repair")
	}
}

func TestRepairEpochZeroHookUsage_HonorsBatchBound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	store := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	sessionTime := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"grok:stop_hook:a", "grok:stop_hook:b", "grok:stop_hook:c"} {
		seedEpochUsageFixture(t, conn, epochUsageFixture{
			observationID: id,
			sessionID:     "session-batch",
			host:          "grok",
			sourceName:    "stop_hook",
			sessionTime:   sessionTime,
			withEvent:     id == "grok:stop_hook:a",
		})
	}

	first, err := sqlite.RepairEpochZeroHookUsageObservations(ctx, conn, 2)
	if err != nil {
		t.Fatalf("first batch error = %v", err)
	}
	if first.Selected != 2 || first.Repaired != 2 || !first.MorePending {
		t.Fatalf("first batch = %+v, want selected=2 repaired=2 more_pending", first)
	}
	second, err := sqlite.RepairEpochZeroHookUsageObservations(ctx, conn, 2)
	if err != nil {
		t.Fatalf("second batch error = %v", err)
	}
	if second.Repaired != 1 || second.MorePending {
		t.Fatalf("second batch = %+v, want repaired=1", second)
	}
}

func TestInitialize_RepairsEpochZeroHookUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	store := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	sessionTime := time.Date(2026, 8, 13, 7, 0, 0, 0, time.UTC)
	seedEpochUsageFixture(t, conn, epochUsageFixture{
		observationID: "claude:stop_hook:init",
		sessionID:     "session-init",
		host:          "claude",
		sourceName:    "stop_hook",
		sessionTime:   sessionTime,
		withEvent:     true,
	})
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := sqlite.NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize(repair) error = %v", err)
	}
	conn, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(reopen) error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	want := sessionTime.UTC().Format(time.RFC3339Nano)
	assertUsageTimes(t, conn, "claude:stop_hook:init", want, want)
}

func TestUsageCaptureUsecases_WriteNonEpochUnavailableObservations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	store := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	if err := sqlite.NewStoreManagementDatasource(store).Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	repo := sqlite.NewUsageObservationDatasource(store)
	epoch := time.Unix(0, 0).UTC()

	if _, err := usecase.NewGrokUsageCaptureUsecase(repo).CaptureHookUnavailable(ctx, usecase.GrokUsageCaptureInput{
		SessionID: "session-1", DeliveryID: "prompt_id:1",
	}); err != nil {
		t.Fatalf("grok capture error = %v", err)
	}
	if _, err := usecase.NewAntigravityUsageCaptureUsecase(nil, repo).CaptureStopUnavailable(ctx, usecase.AntigravityUsageStopInput{
		SessionID: "session-1", BoundaryID: "turn:1",
	}); err != nil {
		t.Fatalf("antigravity capture error = %v", err)
	}
	if _, err := usecase.NewClaudeUsageCaptureUsecase(claudeUsageSourceEmpty{}, repo).Capture(ctx, usecase.ClaudeUsageCaptureInput{
		SessionID: "session-1", DeliveryID: "event_id:stop-1",
	}); err != nil {
		t.Fatalf("claude capture error = %v", err)
	}
	if _, err := usecase.NewCodexUsageCaptureUsecase(codexUsageSourceEmpty{}, repo).Capture(ctx, usecase.CodexUsageCaptureInput{
		SessionID: "session-1", DeliveryID: "event_id:stop-1",
	}); err != nil {
		t.Fatalf("codex capture error = %v", err)
	}
	eventTime := time.Date(2026, 8, 16, 11, 0, 0, 0, time.UTC)
	if _, err := usecase.NewGeminiUsageCaptureUsecase(repo).CaptureInteractiveUnavailable(ctx, usecase.GeminiUsageCaptureInput{
		SessionID: "session-1", DeliveryID: "after-agent-1", EventTime: eventTime,
	}); err != nil {
		t.Fatalf("gemini capture error = %v", err)
	}
	if _, err := usecase.NewKimiUsageCaptureUsecase(kimiUsageSourceEmpty{}, repo).Capture(ctx, usecase.KimiUsageCaptureInput{
		SessionID: "session-1", ProviderSessionID: "provider-1", Boundary: usecase.KimiUsageBoundaryStop,
	}); err != nil {
		t.Fatalf("kimi capture error = %v", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	rows, err := conn.Query(`SELECT observation_id, observed_at, finalized_at, source_name FROM usage_observations`)
	if err != nil {
		t.Fatalf("query usage_observations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var id, observedAt, finalizedAt, sourceName string
		if err := rows.Scan(&id, &observedAt, &finalizedAt, &sourceName); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		parsed, err := time.Parse(time.RFC3339Nano, observedAt)
		if err != nil || !parsed.After(epoch) {
			t.Fatalf("%s observed_at = %s, want after epoch", id, observedAt)
		}
		parsedFinal, err := time.Parse(time.RFC3339Nano, finalizedAt)
		if err != nil || !parsedFinal.After(epoch) {
			t.Fatalf("%s finalized_at = %s, want after epoch", id, finalizedAt)
		}
		if sourceName == "after_agent_hook" && !parsed.Equal(eventTime) {
			t.Fatalf("gemini observed_at = %s, want %s", observedAt, eventTime)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("observations = %d, want 6 host writes", count)
	}
}

type claudeUsageSourceEmpty struct{}

func (claudeUsageSourceEmpty) Load(
	context.Context,
	application.ClaudeUsageLoadCriteria,
) (application.ClaudeUsageLoadResult, error) {
	return application.ClaudeUsageLoadResult{Mode: application.ClaudeUsageModeTranscriptCalls}, nil
}

type codexUsageSourceEmpty struct{}

func (codexUsageSourceEmpty) Load(
	context.Context,
	application.CodexUsageLoadCriteria,
) (application.CodexUsageLoadResult, error) {
	return application.CodexUsageLoadResult{}, nil
}

type kimiUsageSourceEmpty struct{}

func (kimiUsageSourceEmpty) Load(context.Context, string) (application.KimiUsageLoadResult, error) {
	return application.KimiUsageLoadResult{}, nil
}

type epochUsageFixture struct {
	observationID string
	sessionID     string
	host          string
	sourceName    string
	sessionTime   time.Time
	withEvent     bool
}

func seedEpochUsageFixture(t *testing.T, db *sql.DB, fixture epochUsageFixture) {
	t.Helper()
	epoch := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	if !fixture.sessionTime.IsZero() {
		if _, err := db.Exec(`
INSERT OR IGNORE INTO sessions (session_id, started_at, ended_at, client, agent, workspace)
VALUES (?, ?, ?, 'test', 'test', 'ws')`,
			fixture.sessionID, fixture.sessionTime.Add(-time.Hour).UTC().Format(time.RFC3339Nano), fixture.sessionTime.UTC().Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("insert session: %v", err)
		}
	}
	if fixture.withEvent {
		if _, err := db.Exec(`
INSERT INTO events (id, kind, agent, session_id, body, created_at, client, workspace)
VALUES (?, 'session_ended', 'test', ?, 'session ended', ?, 'test', 'ws')`,
			"event-"+fixture.observationID, fixture.sessionID, fixture.sessionTime.UTC().Format(time.RFC3339Nano),
		); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	if _, err := db.Exec(`
INSERT INTO usage_observations (
    observation_id, session_id, host, source_name, source_version,
    scope, accounting, status, observed_at, finalized_at, terminal_code,
    input_state, cached_input_state, cache_write_input_state,
    output_state, reasoning_output_state, total_state, cost_state
) VALUES (?, ?, ?, ?, 'schema-v1',
    'call', 'excluded', 'finalized', ?, ?, 'unknown',
    'unavailable', 'unavailable', 'unavailable',
    'unavailable', 'unavailable', 'unavailable', 'unavailable')`,
		fixture.observationID, fixture.sessionID, fixture.host, fixture.sourceName, epoch, epoch,
	); err != nil {
		t.Fatalf("insert usage observation %s: %v", fixture.observationID, err)
	}
}

func assertUsageTimes(t *testing.T, db *sql.DB, id, wantObserved, wantFinalized string) {
	t.Helper()
	var observedAt, finalizedAt string
	if err := db.QueryRow(
		`SELECT observed_at, finalized_at FROM usage_observations WHERE observation_id = ?`,
		id,
	).Scan(&observedAt, &finalizedAt); err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	if observedAt != wantObserved || finalizedAt != wantFinalized {
		t.Fatalf("%s times = (%s, %s), want (%s, %s)", id, observedAt, finalizedAt, wantObserved, wantFinalized)
	}
}

var _ = types.SessionID("")
