package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
    body_availability TEXT NOT NULL DEFAULT 'available',
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

func TestDatasource_SaveWithAuditCanonicalPayloadPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	sut, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	newEvent := func(id, body string) *model.Event {
		return model.EventOf(types.EventID(id), types.EventKindCommandExecuted, "cli", types.Agent("codex"), types.SessionID("session"), "workspace", body, time.Now().UTC())
	}
	save := func(event *model.Event, command, input, output string) {
		audit, err := model.NewCommandAudit(event.EventID(), command, input, output, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := sut.SaveWithAudit(ctx, event, audit); err != nil {
			t.Fatal(err)
		}
	}
	compressedBody := strings.Repeat("canonical payload should compress ", 256)
	// Pre-trimmed: NewCommandAudit trims the command, and the checksum below is
	// over what is stored, not over what was handed to the constructor.
	compressedCommand := strings.TrimSpace(strings.Repeat("command ", 128))
	compressedInput := strings.Repeat("input ", 128)
	compressedOutput := strings.Repeat("output ", 128)
	save(newEvent("compressed", compressedBody), compressedCommand, compressedInput, compressedOutput)
	save(newEvent("short", "x"), "x", "y", "z")
	// input_text and output_text are frequently empty in practice. An empty
	// value must stay identity -- a zstd frame around nothing is still several
	// bytes, which would grow it -- and must stay TEXT so it reads back as an
	// empty string rather than as a zero-length BLOB.
	save(newEvent("empty", "x"), "x", "", "")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Every payload lane, not only the two pinned first: a lane that stopped
	// going through storedBodyArg would still round trip in process while
	// storing an affinity the body derivation triggers cannot match.
	storage := []struct {
		name       string
		query      string
		wantCodec  string
		wantTypeOf string
	}{
		{name: "compressed event body", query: `SELECT body_codec, typeof(body) FROM events WHERE id='compressed'`, wantCodec: "zstd", wantTypeOf: "blob"},
		{name: "compressed audit command", query: `SELECT command_codec, typeof(command_text) FROM command_audits WHERE event_id='compressed'`, wantCodec: "zstd", wantTypeOf: "blob"},
		{name: "compressed audit input", query: `SELECT input_codec, typeof(input_text) FROM command_audits WHERE event_id='compressed'`, wantCodec: "zstd", wantTypeOf: "blob"},
		{name: "compressed audit output", query: `SELECT output_codec, typeof(output_text) FROM command_audits WHERE event_id='compressed'`, wantCodec: "zstd", wantTypeOf: "blob"},
		{name: "short event body", query: `SELECT body_codec, typeof(body) FROM events WHERE id='short'`, wantCodec: "identity", wantTypeOf: "text"},
		{name: "short audit command", query: `SELECT command_codec, typeof(command_text) FROM command_audits WHERE event_id='short'`, wantCodec: "identity", wantTypeOf: "text"},
		{name: "empty audit input", query: `SELECT input_codec, typeof(input_text) FROM command_audits WHERE event_id='empty'`, wantCodec: "identity", wantTypeOf: "text"},
		{name: "empty audit output", query: `SELECT output_codec, typeof(output_text) FROM command_audits WHERE event_id='empty'`, wantCodec: "identity", wantTypeOf: "text"},
	}
	for _, tt := range storage {
		var codec, sqliteType string
		if err := db.QueryRow(tt.query).Scan(&codec, &sqliteType); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if diff := cmp.Diff([]string{tt.wantCodec, tt.wantTypeOf}, []string{codec, sqliteType}); diff != "" {
			t.Errorf("%s storage mismatch (-want +got):\n%s", tt.name, diff)
		}
	}

	// Integrity metadata is what makes a compressed row readable at all:
	// payloadRow.decode rejects a row whose encoded byte count disagrees with
	// the stored value, or whose checksum is not the checksum of the plaintext.
	// Pinning plaintext provenance separately from the encoded size is the
	// point -- writing the compressed size into either would fail every read.
	metadata := []struct {
		name      string
		query     string
		plaintext string
	}{
		{name: "event body", query: `SELECT body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256, length(CAST(body AS BLOB)) FROM events WHERE id='compressed'`, plaintext: compressedBody},
		{name: "audit command", query: `SELECT command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256, length(CAST(command_text AS BLOB)) FROM command_audits WHERE event_id='compressed'`, plaintext: compressedCommand},
		{name: "audit input", query: `SELECT input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256, length(CAST(input_text AS BLOB)) FROM command_audits WHERE event_id='compressed'`, plaintext: compressedInput},
		{name: "audit output", query: `SELECT output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256, length(CAST(output_text AS BLOB)) FROM command_audits WHERE event_id='compressed'`, plaintext: compressedOutput},
	}
	for _, tt := range metadata {
		var formatVersion, plaintextBytes, encodedBytes, storedLength int64
		var checksum string
		if err := db.QueryRow(tt.query).Scan(&formatVersion, &plaintextBytes, &encodedBytes, &checksum, &storedLength); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		sum := sha256.Sum256([]byte(tt.plaintext))
		// 1 is payloadFormatVersion, spelled out because this is an external
		// test package and the constant is the persisted contract anyway.
		want := []int64{1, int64(len(tt.plaintext)), storedLength}
		if diff := cmp.Diff(want, []int64{formatVersion, plaintextBytes, encodedBytes}); diff != "" {
			t.Errorf("%s integrity metadata mismatch (-want +got):\n%s", tt.name, diff)
		}
		if diff := cmp.Diff(hex.EncodeToString(sum[:]), checksum); diff != "" {
			t.Errorf("%s checksum mismatch (-want +got):\n%s", tt.name, diff)
		}
		if encodedBytes >= plaintextBytes {
			t.Errorf("%s encoded bytes = %d, want fewer than plaintext %d", tt.name, encodedBytes, plaintextBytes)
		}
	}

	details, err := sut.GetDetails(ctx, types.EventID("compressed"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(compressedBody, details.Event().Body()); diff != "" {
		t.Fatalf("compressed body round trip mismatch (-want +got):\n%s", diff)
	}
	audit, ok := details.CommandAudit().Value()
	if !ok {
		t.Fatal("compressed audit missing")
	}
	if diff := cmp.Diff([]string{compressedCommand, compressedInput, compressedOutput}, []string{audit.Command(), audit.Input(), audit.Output()}); diff != "" {
		t.Fatalf("compressed audit round trip mismatch (-want +got):\n%s", diff)
	}
	emptyDetails, err := sut.GetDetails(ctx, types.EventID("empty"))
	if err != nil {
		t.Fatal(err)
	}
	emptyAudit, ok := emptyDetails.CommandAudit().Value()
	if !ok {
		t.Fatal("empty audit missing")
	}
	if diff := cmp.Diff([]string{"", ""}, []string{emptyAudit.Input(), emptyAudit.Output()}); diff != "" {
		t.Fatalf("empty audit round trip mismatch (-want +got):\n%s", diff)
	}
	matches, err := sut.Search(ctx, "canonical payload", "", "", "", "", "", time.Time{}, time.Time{}, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 || matches[0].EventID() != types.EventID("compressed") {
		t.Fatalf("compressed search matches = %#v, want compressed event", matches)
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
	if err := currentStore.InitializeAuthorized(ctx); err != nil {
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
    body_availability TEXT NOT NULL DEFAULT 'available',
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
