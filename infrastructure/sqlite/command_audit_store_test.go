package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestDatasource_SaveWithAudit(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    source_hook TEXT
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    command_wrapper TEXT NOT NULL DEFAULT '',
    command_name TEXT NOT NULL DEFAULT 'unknown',
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    input_original_bytes INTEGER NOT NULL DEFAULT 0,
    output_original_bytes INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER,
    failed INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT 'unknown'
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	eventID, err := types.EventIDFrom("event-1")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	event := model.EventOf(
		eventID,
		types.EventKindCommandExecuted,
		"cli",
		agent,
		sessionID,
		"duck8823/traceary",
		"rtk git status",
		time.Date(2026, 4, 7, 14, 0, 0, 0, time.UTC),
	)
	commandAudit, err := model.NewCommandAudit(
		eventID,
		"rtk git status",
		"stdin",
		"stdout",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	if err := commandAudit.ClassifyOutcome(types.None[int](), types.CommandFailureReasonHookDenied, false); err != nil {
		t.Fatalf("ClassifyOutcome() error = %v", err)
	}

	if err := sut.SaveWithAudit(context.Background(), event, commandAudit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var (
		kind            string
		commandText     string
		commandWrapper  string
		commandName     string
		inputTruncated  bool
		outputTruncated bool
		failureReason   string
	)
	if err := db.QueryRow(`
SELECT e.kind, a.command_text, a.command_wrapper, a.command_name,
       a.input_truncated, a.output_truncated, a.failure_reason
  FROM command_audits a
  JOIN events e ON e.id = a.event_id
 WHERE a.event_id = ?`,
		"event-1",
	).Scan(&kind, &commandText, &commandWrapper, &commandName, &inputTruncated, &outputTruncated, &failureReason); err != nil {
		t.Fatalf("audit query error = %v", err)
	}
	if diff := cmp.Diff("command_executed", kind); diff != "" {
		t.Fatalf("kind mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("rtk git status", commandText); diff != "" {
		t.Fatalf("command_text mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("rtk", commandWrapper); diff != "" {
		t.Fatalf("command_wrapper mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("git", commandName); diff != "" {
		t.Fatalf("command_name mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("hook_denied", failureReason); diff != "" {
		t.Fatalf("failure_reason mismatch (-want +got):\n%s", diff)
	}
	if !inputTruncated {
		t.Fatalf("input_truncated = false, want true")
	}
	if outputTruncated {
		t.Fatalf("output_truncated = true, want false")
	}
}

func TestCommandAuditNormalizationMigrationPreservesLegacyUnknowns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	_, legacyStore := newEventDatasource(t, dbPath, onDiskSQLiteMigrationsBefore(t, 25))
	if err := legacyStore.Initialize(ctx); err != nil {
		t.Fatalf("legacy Initialize() error = %v", err)
	}
	eventID := types.EventID("legacy-command")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at)
VALUES (?, 'command_executed', 'hook', 'codex', 'legacy-session', 'workspace', 'rtk git status', ?)`, eventID.String(), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed)
VALUES (?, 'rtk git status', '', '{"failed": true}', 0, 0, 0, 0, 0, 1)`, eventID.String()); err != nil {
		t.Fatalf("insert legacy audit: %v", err)
	}
	_ = legacyDB.Close()

	currentEvents, currentStore := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := currentStore.Initialize(ctx); err != nil {
		t.Fatalf("current Initialize() error = %v", err)
	}
	details, err := currentEvents.GetDetails(ctx, eventID)
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	got, ok := details.CommandAudit().Value()
	if !ok {
		t.Fatal("CommandAudit() missing")
	}
	if got.CommandIdentity().Command() != types.CommandNameUnknown {
		t.Fatalf("CommandName = %q, want unknown", got.CommandIdentity().Command())
	}
	if _, wrapper := got.CommandIdentity().Wrapper().Value(); wrapper {
		t.Fatal("Wrapper() present for legacy row")
	}
	if got.FailureReason() != types.CommandFailureReasonUnknown {
		t.Fatalf("FailureReason() = %q, want unknown", got.FailureReason())
	}
	if !got.Failed() {
		t.Fatal("legacy failed flag was not preserved")
	}
}

func TestDatasource_SaveWithAudit_PreservesEqualAuditsWithoutDeliveryIdentity(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
	save := func(id string, at time.Time, output string) {
		t.Helper()

		eventID, err := types.EventIDFrom(id)
		if err != nil {
			t.Fatalf("EventIDFrom(%s) error = %v", id, err)
		}
		agent, err := types.AgentFrom("codex")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}
		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		event := model.EventOfWithSourceHook(
			eventID,
			types.EventKindCommandExecuted,
			types.Client("hook"),
			agent,
			sessionID,
			types.Workspace("github.com/duck8823/dotfiles"),
			"git status\n\nINPUT:\n{\"command\":\"git status\"}\n\nOUTPUT:\n"+output,
			at,
			"post_tool_use",
		)
		audit, err := model.NewCommandAudit(eventID, "git status", `{"command":"git status"}`, output, false, false)
		if err != nil {
			t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
		}
		if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
			t.Fatalf("SaveWithAudit(%s) error = %v", id, err)
		}
	}

	save("event-original", base, "same output")
	save("event-duplicate", base.Add(time.Second), "same output")
	save("event-different-output", base.Add(2*time.Second), "different output")
	save("event-later-rerun", base.Add(10*time.Second), "same output")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_audits`).Scan(&count); err != nil {
		t.Fatalf("command audit count query error = %v", err)
	}
	if diff := cmp.Diff(4, count); diff != "" {
		t.Fatalf("command audit count mismatch (-want +got):\n%s", diff)
	}

	var duplicateRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = 'event-duplicate'`).Scan(&duplicateRows); err != nil {
		t.Fatalf("duplicate event count query error = %v", err)
	}
	if diff := cmp.Diff(1, duplicateRows); diff != "" {
		t.Fatalf("duplicate event persisted mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_SaveWithAudit_DoesNotDeduplicateDifferentOriginalPayloadSizes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	base := time.Date(2026, 5, 31, 1, 0, 0, 0, time.UTC)
	save := func(id string, at time.Time, originalBytes int) {
		t.Helper()

		eventID, err := types.EventIDFrom(id)
		if err != nil {
			t.Fatalf("EventIDFrom(%s) error = %v", id, err)
		}
		agent, err := types.AgentFrom("codex")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}
		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		const truncatedOutput = "same-truncated-output"
		event := model.EventOfWithSourceHook(
			eventID,
			types.EventKindCommandExecuted,
			types.Client("hook"),
			agent,
			sessionID,
			types.Workspace("github.com/duck8823/dotfiles"),
			"git status\n\nOUTPUT (truncated):\n"+truncatedOutput,
			at,
			"post_tool_use",
		)
		audit, err := model.NewCommandAudit(eventID, "git status", "", truncatedOutput, false, true)
		if err != nil {
			t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
		}
		audit.SetOriginalPayloadBytes(0, originalBytes)
		if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
			t.Fatalf("SaveWithAudit(%s) error = %v", id, err)
		}
	}

	save("event-truncated-a", base, 1000)
	save("event-truncated-b", base.Add(time.Second), 2000)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_audits`).Scan(&count); err != nil {
		t.Fatalf("command audit count query error = %v", err)
	}
	if diff := cmp.Diff(2, count); diff != "" {
		t.Fatalf("command audit count mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_AuditLargeOutputPersistsBoundedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	longOutput := "output-head-" + strings.Repeat("o", 1024*1024) + "-output-tail"
	sut := usecase.NewEventUsecase(eventDS, nil)
	event, _, err := sut.Audit(ctx,
		apptypes.AuditInput{
			Command:   "printf large-output",
			Input:     "",
			Output:    longOutput,
			Client:    types.Client("cli"),
			Agent:     types.Agent("codex"),
			SessionID: types.SessionID("session-large"),
			Workspace: types.Workspace("duck8823/traceary"),
			ExitCode:  types.None[int](),
			Failed:    false,
		},
		apptypes.NewAuditRedactionBuilder().
			MaxOutputBytes(4096).
			Build(),
	)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var outputTextBytes, outputOriginalBytes, eventBodyBytes int
	if err := db.QueryRow(`SELECT length(output_text) FROM command_audits WHERE event_id = ?`, event.EventID().String()).Scan(&outputTextBytes); err != nil {
		t.Fatalf("command_audits length query error = %v", err)
	}
	if err := db.QueryRow(`SELECT output_original_bytes FROM command_audits WHERE event_id = ?`, event.EventID().String()).Scan(&outputOriginalBytes); err != nil {
		t.Fatalf("command_audits original bytes query error = %v", err)
	}
	if err := db.QueryRow(`SELECT length(body) FROM events WHERE id = ?`, event.EventID().String()).Scan(&eventBodyBytes); err != nil {
		t.Fatalf("events length query error = %v", err)
	}
	if outputTextBytes > 4096 {
		t.Fatalf("output_text length = %d, want <= 4096", outputTextBytes)
	}
	if outputOriginalBytes != len(longOutput) {
		t.Fatalf("output_original_bytes = %d, want %d", outputOriginalBytes, len(longOutput))
	}
	if eventBodyBytes > 4600 {
		t.Fatalf("event body length = %d, want <= 4600", eventBodyBytes)
	}

	details, err := eventDS.GetDetails(ctx, event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	audit, ok := details.CommandAudit().Value()
	if !ok {
		t.Fatalf("CommandAudit() is empty, want command audit")
	}
	if !audit.OutputTruncated() {
		t.Fatalf("OutputTruncated() = false, want true")
	}
	if audit.OutputOriginalBytes() != len(longOutput) {
		t.Fatalf("OutputOriginalBytes() = %d, want %d", audit.OutputOriginalBytes(), len(longOutput))
	}
	if len(audit.Output()) > 4096 {
		t.Fatalf("len(Output()) = %d, want <= 4096", len(audit.Output()))
	}
	for _, want := range []string{"output-head-", "-output-tail", "truncated original_bytes="} {
		if !strings.Contains(audit.Output(), want) {
			t.Fatalf("Output() missing %q", want)
		}
	}
}

// TestDatasource_ListRecent_FailuresOnly_IncludesFailedFlag verifies the
// failures filter treats the structural failed flag as a failure even when no
// numeric exit code was captured — the Claude PostToolUseFailure case, where
// the hook payload carries an "error" string but no exit code. The legacy
// non-zero exit_code path must still match, and successes must be excluded.
func TestDatasource_ListRecent_FailuresOnly_IncludesFailedFlag(t *testing.T) {
	t.Parallel()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    source_hook TEXT
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    command_wrapper TEXT NOT NULL DEFAULT '',
    command_name TEXT NOT NULL DEFAULT 'unknown',
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    input_original_bytes INTEGER NOT NULL DEFAULT 0,
    output_original_bytes INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER,
    failed INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT 'unknown'
);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	save := func(id, command string, exitCode types.Optional[int], failed bool) {
		eventID, err := types.EventIDFrom(id)
		if err != nil {
			t.Fatalf("EventIDFrom(%s) error = %v", id, err)
		}
		agent, err := types.AgentFrom("claude")
		if err != nil {
			t.Fatalf("AgentFrom() error = %v", err)
		}
		sessionID, err := types.SessionIDFrom("session-1")
		if err != nil {
			t.Fatalf("SessionIDFrom() error = %v", err)
		}
		event := model.EventOf(
			eventID,
			types.EventKindCommandExecuted,
			"hook",
			agent,
			sessionID,
			"duck8823/traceary",
			command,
			time.Date(2026, 4, 7, 14, 0, 0, 0, time.UTC),
		)
		audit, err := model.NewCommandAudit(eventID, command, "stdin", "stdout", false, false)
		if err != nil {
			t.Fatalf("NewCommandAudit(%s) error = %v", id, err)
		}
		audit.SetExitCode(exitCode)
		audit.SetFailed(failed)
		if err := sut.SaveWithAudit(context.Background(), event, audit); err != nil {
			t.Fatalf("SaveWithAudit(%s) error = %v", id, err)
		}
	}

	save("event-failed-flag", "failed tool without exit code", types.None[int](), true)
	save("event-nonzero-exit", "command with nonzero exit", types.Some(1), false)
	save("event-success", "successful command", types.None[int](), false)

	events, err := sut.ListRecent(context.Background(), 50, 0, "", "", "", "", "", true, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent(failuresOnly) error = %v", err)
	}

	got := map[string]bool{}
	for _, e := range events {
		got[e.EventID().String()] = true
	}
	if !got["event-failed-flag"] {
		t.Fatalf("failuresOnly omitted failed-flag row (failed=true, exit_code NULL); got %v", got)
	}
	if !got["event-nonzero-exit"] {
		t.Fatalf("failuresOnly omitted nonzero-exit row; got %v", got)
	}
	if got["event-success"] {
		t.Fatalf("failuresOnly returned a success row; got %v", got)
	}
}

func TestDatasource_SaveWithAudit_DoesNotInferIdentityFromFractionalTimestamps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	workspace := types.Workspace("github.com/duck8823/dotfiles")
	output := "On branch main\n"

	base := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC).Add(500 * time.Millisecond)

	save := func(id string, at time.Time) {
		t.Helper()
		eventID, err := types.EventIDFrom(id)
		if err != nil {
			t.Fatalf("EventIDFrom(%q) error = %v", id, err)
		}
		event := model.EventOfWithSourceHook(eventID, types.EventKindCommandExecuted, types.Client("hook"), agent, sessionID, workspace, "git status\n\nINPUT:\n{}\n\nOUTPUT:\n"+output, at, "post_tool_use")
		audit, err := model.NewCommandAudit(eventID, "git status", "", output, false, false)
		if err != nil {
			t.Fatalf("NewCommandAudit() error = %v", err)
		}
		if err := sut.SaveWithAudit(ctx, event, audit); err != nil {
			t.Fatalf("SaveWithAudit() error = %v", err)
		}
	}

	countAudits := func() int {
		t.Helper()
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open() error = %v", err)
		}
		defer func() { _ = db.Close() }()
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM command_audits").Scan(&count); err != nil {
			t.Fatalf("QueryRowContext() error = %v", err)
		}
		return count
	}

	save("event-1", base)
	save("event-2", base.Add(1500*time.Millisecond))

	if diff := cmp.Diff(2, countAudits()); diff != "" {
		t.Errorf("command_audits count after duplicate mismatch (-want +got):\n%s", diff)
	}

	save("event-3", base.Add(2500*time.Millisecond))

	if diff := cmp.Diff(3, countAudits()); diff != "" {
		t.Errorf("command_audits count after distinct entry mismatch (-want +got):\n%s", diff)
	}
}
