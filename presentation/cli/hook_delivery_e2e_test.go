package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application/usecase"
	sqliteinfra "github.com/duck8823/traceary/infrastructure/sqlite"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HookAntigravityStopReplayUsesStableDeliveryLedger(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "github.com/dogfood/antigravity-stop")

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	identityDS := sqliteinfra.NewWorkspaceIdentityDatasource(db)
	identityUC := usecase.NewWorkspaceIdentityUsecase(identityDS, identityDS, nil)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	transcriptPath := filepath.Join("testdata", "antigravity_hooks", "current", "stop_transcript.jsonl")
	payload := fmt.Sprintf(
		`{"conversationId":"antigravity-stop-replay","workspacePaths":["/dogfood/antigravity-stop"],"transcriptPath":%q,"terminationReason":"completed"}`,
		transcriptPath,
	)
	fire := func() {
		t.Helper()
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithEvent(eventUC),
			cli.WithSession(sessionUC),
			cli.WithWorkspaceIdentity(identityUC),
			cli.WithDatabasePathSetter(db.SetPath),
		).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"hook", "antigravity", "stop", "--db-path", dbPath})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(hook antigravity stop) error = %v", err)
		}
		if got := stdout.String(); got != `{"decision":""}` {
			t.Fatalf("Stop output = %q, want decision contract", got)
		}
	}
	fire()
	fire()

	sqldb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqldb.Close() }()

	for _, tc := range []struct {
		kind       string
		sourceHook string
	}{
		{kind: "prompt", sourceHook: "stop_transcript"},
		{kind: "transcript", sourceHook: "stop"},
	} {
		var count int
		if err := sqldb.QueryRow(
			`SELECT COUNT(*) FROM events WHERE session_id = ? AND kind = ? AND source_hook = ?`,
			"antigravity-stop-replay", tc.kind, tc.sourceHook,
		).Scan(&count); err != nil {
			t.Fatalf("count %s events: %v", tc.kind, err)
		}
		if count != 1 {
			t.Fatalf("%s events = %d, want one logical event after replay", tc.kind, count)
		}
	}
	var boundaryCount int
	if err := sqldb.QueryRow(
		`SELECT COUNT(*) FROM events WHERE session_id = ? AND kind IN ('session_started', 'session_ended')`,
		"antigravity-stop-replay",
	).Scan(&boundaryCount); err != nil {
		t.Fatalf("count session boundaries: %v", err)
	}
	if boundaryCount != 0 {
		t.Fatalf("Stop session boundary events = %d, want zero", boundaryCount)
	}

	var accepted, exact, conflicts int
	if err := sqldb.QueryRow(`
		SELECT
			SUM(outcome = 'accepted'),
			SUM(outcome = 'exact_redelivery'),
			SUM(outcome = 'conflict')
		FROM hook_delivery_attempts
		WHERE attempt_origin = 'runtime'
	`).Scan(&accepted, &exact, &conflicts); err != nil {
		t.Fatalf("read delivery attempts: %v", err)
	}
	if accepted != 2 || exact != 2 || conflicts != 0 {
		t.Fatalf("delivery attempts accepted/exact/conflict = %d/%d/%d, want 2/2/0", accepted, exact, conflicts)
	}

	reportCmd := cli.NewRootCLI(
		cli.WithStoreManagement(storeUC),
		cli.WithWorkspaceIdentity(identityUC),
		cli.WithDatabasePathSetter(db.SetPath),
	).Command()
	reportOut := &bytes.Buffer{}
	reportCmd.SetOut(reportOut)
	reportCmd.SetErr(&bytes.Buffer{})
	reportCmd.SetArgs([]string{"report", "workspace-identity", "--db-path", dbPath, "--include-heuristic", "--json"})
	if err := reportCmd.Execute(); err != nil {
		t.Fatalf("Execute(report workspace-identity) error = %v", err)
	}
	var report struct {
		ExactDelivery struct {
			AttemptCount         int     `json:"attempt_count"`
			ExactRedeliveryCount int     `json:"exact_redelivery_count"`
			ExactRedeliveryRate  float64 `json:"exact_redelivery_rate"`
		} `json:"exact_delivery"`
		Heuristic struct {
			CandidateCount int `json:"candidate_count"`
		} `json:"heuristic_candidates"`
	}
	if err := json.Unmarshal(reportOut.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal(report) error = %v\n%s", err, reportOut.String())
	}
	if report.ExactDelivery.AttemptCount != 4 || report.ExactDelivery.ExactRedeliveryCount != 2 || report.ExactDelivery.ExactRedeliveryRate != 0.5 {
		t.Fatalf("exact delivery report = %+v, want attempts=4 exact=2 rate=0.5", report.ExactDelivery)
	}
	if report.Heuristic.CandidateCount != 0 {
		t.Fatalf("heuristic candidates = %d, want zero for suppressed stable-ID replay", report.Heuristic.CandidateCount)
	}
}

func TestRootCLI_HookSessionRedeliveryKeepsCanonicalWorkspace(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_WORKSPACE", "")
	canonicalDir := t.TempDir()
	retryDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	ctx := context.Background()
	db := sqliteinfra.NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	eventDS := sqliteinfra.NewEventDatasource(db)
	sessionDS := sqliteinfra.NewSessionDatasource(db)
	storeUC := usecase.NewStoreManagementUsecase(sqliteinfra.NewStoreManagementDatasource(db))
	eventUC := usecase.NewEventUsecase(eventDS, eventDS)
	sessionUC := usecase.NewSessionUsecase(eventDS, sessionDS, sessionDS, eventDS)
	if err := storeUC.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	fire := func(args []string, payload string) {
		t.Helper()
		rootCmd := cli.NewRootCLI(
			cli.WithStoreManagement(storeUC),
			cli.WithEvent(eventUC),
			cli.WithSession(sessionUC),
			cli.WithDatabasePathSetter(db.SetPath),
		).Command()
		rootCmd.SetIn(strings.NewReader(payload))
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs(append(args, "--db-path", dbPath))
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}

	fire([]string{"hook", "session", "codex", "start"}, fmt.Sprintf(`{"session_id":"session-canonical","cwd":%q}`, canonicalDir))
	fire([]string{"hook", "session", "codex", "start"}, fmt.Sprintf(`{"session_id":"session-canonical","cwd":%q}`, retryDir))
	fire([]string{"hook", "prompt", "codex"}, `{"session_id":"session-canonical","event_id":"prompt-after-retry","prompt":"continue without cwd"}`)

	sqldb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = sqldb.Close() }()
	var canonical, promptWorkspace string
	if err := sqldb.QueryRow(`SELECT workspace FROM sessions WHERE session_id = 'session-canonical'`).Scan(&canonical); err != nil {
		t.Fatalf("read canonical session: %v", err)
	}
	if err := sqldb.QueryRow(`SELECT workspace FROM events WHERE kind = 'prompt' AND session_id = 'session-canonical'`).Scan(&promptWorkspace); err != nil {
		t.Fatalf("read prompt workspace: %v", err)
	}
	if canonical == "" || promptWorkspace != canonical {
		t.Fatalf("canonical/prompt workspace = %q/%q, want identical non-empty canonical fallback", canonical, promptWorkspace)
	}
	var starts, supplemental int
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'session_started' AND session_id = 'session-canonical'`).Scan(&starts); err != nil {
		t.Fatalf("count session starts: %v", err)
	}
	if err := sqldb.QueryRow(`SELECT COUNT(*) FROM session_workspace_observations WHERE observation_kind = 'supplemental' AND session_id = 'session-canonical'`).Scan(&supplemental); err != nil {
		t.Fatalf("count supplemental observations: %v", err)
	}
	if starts != 1 || supplemental != 1 {
		t.Fatalf("session starts/supplemental = %d/%d, want 1/1", starts, supplemental)
	}
}
