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
	"github.com/duck8823/traceary/domain/attestation"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestVerifyAttestationChain_EmptyStoreIsGenesis(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)
	_ = events

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v", err)
	}
	var head string
	var seq int64
	if err := db.QueryRow(`SELECT head_sha256, seq FROM attestation_head WHERE singleton = 1`).Scan(&head, &seq); err != nil {
		t.Fatalf("read head: %v", err)
	}
	if seq != 0 {
		t.Fatalf("seq = %d, want 0", seq)
	}
	if head != attestation.GenesisHex() {
		t.Fatalf("head = %q, want genesis", head)
	}
}

func TestVerifyAttestationChain_SavePromptThenSaveWithAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)

	prompt := model.EventOf(
		types.EventID("prompt-1"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"please run tests",
		time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save(prompt) error = %v", err)
	}

	command := model.EventOf(
		types.EventID("command-1"), types.EventKindCommandExecuted,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"",
		time.Date(2026, 8, 14, 10, 0, 1, 0, time.UTC),
	)
	audit, err := model.NewCommandAudit(command.EventID(), "go test ./...", "", "ok", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	if err := events.SaveWithAudit(ctx, command, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v", err)
	}
	var seq int64
	if err := db.QueryRow(`SELECT seq FROM attestation_head WHERE singleton = 1`).Scan(&seq); err != nil {
		t.Fatalf("read head seq: %v", err)
	}
	if seq != 2 {
		t.Fatalf("head seq = %d, want 2", seq)
	}
	assertAttestationCount(t, db, 2)
}

func TestVerifyAttestationChain_CommandTextTamperFailsAndOutputDoesNot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)

	command := model.EventOf(
		types.EventID("command-tamper"), types.EventKindCommandExecuted,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"",
		time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC),
	)
	audit, err := model.NewCommandAudit(command.EventID(), "ls", "dir", "file.txt", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}
	if err := events.SaveWithAudit(ctx, command, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE command_audits SET output_text = 'tampered-output' WHERE event_id = 'command-tamper'`); err != nil {
		t.Fatalf("UPDATE output_text: %v", err)
	}
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain after output_text UPDATE error = %v", err)
	}
	if _, err := db.Exec(`UPDATE command_audits SET command_text = 'rm' WHERE event_id = 'command-tamper'`); err != nil {
		t.Fatalf("UPDATE command_text: %v", err)
	}
	if err := sqlite.VerifyAttestationChain(ctx, db); err == nil {
		t.Fatal("VerifyAttestationChain() error = nil after command_text UPDATE")
	}
}

func TestVerifyAttestationChain_HistoricalPromptWithoutLinkSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, path, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db := openAttestationDB(t, path)
	if _, err := db.Exec(`
		INSERT INTO events (id, kind, client, agent, session_id, workspace, body, created_at)
		VALUES ('historical-prompt', 'prompt', 'cli', 'codex', 'session-1', 'ws', 'old prompt', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert historical prompt: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	prompt := model.EventOf(
		types.EventID("new-prompt"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"new prompt",
		time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save(new prompt) error = %v", err)
	}

	db = openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v", err)
	}
	assertAttestationCount(t, db, 1)
}

func TestAppendAttestationLink_MissingTableIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, path, onDiskSQLiteMigrationsBefore(t, 61))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-61) error = %v", err)
	}
	prompt := model.EventOf(
		types.EventID("pre61-prompt"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"before attestation",
		time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save on pre-61 store error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='attestation_links'`).Scan(&present); err != nil {
		t.Fatalf("inspect schema: %v", err)
	}
	if present != 0 {
		t.Fatal("pre-61 store must not have attestation_links")
	}
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain on pre-61 store error = %v", err)
	}
}

func TestAppendAttestationLink_ExactRedeliveryDoesNotAddALink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)

	first := hookDeliveryTestEvent(t, "event-1", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")
	retry := hookDeliveryTestEvent(t, "event-2", "session-1", "/repo", "/repo", "same body", "event_id:delivery-1")
	if err := events.Save(ctx, first); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}
	if err := events.Save(ctx, retry); err != nil {
		t.Fatalf("Save(retry) error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v", err)
	}
	assertAttestationCount(t, db, 1)
	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("events = %d, want 1", eventCount)
	}
}

func TestCompactKeepsAttestedHookDuplicatePrompts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)

	first := hookPromptAt(t, "evt-a1", "session-dup", "same prompt", time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC), "event_id:d1")
	second := hookPromptAt(t, "evt-a2", "session-dup", "same prompt", time.Date(2026, 8, 14, 13, 0, 1, 0, time.UTC), "event_id:d2")
	if err := events.Save(ctx, first); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}
	if err := events.Save(ctx, second); err != nil {
		t.Fatalf("Save(second) error = %v", err)
	}

	svc := usecase.NewStoreCompactionUsecase(
		path,
		&sqlite.CompactionFileJournal{Dir: filepath.Join(t.TempDir(), "journal")},
		sqlite.SQLiteCompactionBuilder{},
		sqlite.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		sqlite.StoreLeaseCoordinator{},
	)
	if _, err := svc.Compact(ctx, application.CompactInput{
		Source:   path,
		KeepDays: 90,
		Now:      time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	for _, id := range []string{"evt-a1", "evt-a2"} {
		var one int
		if err := db.QueryRow(`SELECT 1 FROM events WHERE id = ?`, id).Scan(&one); err != nil {
			t.Fatalf("event %s missing after compact: %v", id, err)
		}
	}
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain after compact error = %v", err)
	}
	assertAttestationCount(t, db, 2)
}

func newAttestationTestStore(t *testing.T) (string, *sqlite.EventDatasource) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, path, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return path, events
}

func openAttestationDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return db
}

func assertAttestationCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attestation_links`).Scan(&got); err != nil {
		t.Fatalf("count attestation_links: %v", err)
	}
	if got != want {
		t.Fatalf("attestation_links = %d, want %d", got, want)
	}
}

func hookPromptAt(t *testing.T, eventID, sessionID, body string, at time.Time, nativeID string) *model.Event {
	t.Helper()
	event := model.EventOfWithSourceHook(
		types.EventID(eventID),
		types.EventKindPrompt,
		types.Client("hook"),
		types.Agent("codex"),
		types.SessionID(sessionID),
		types.Workspace("ws"),
		body,
		at,
		"user_prompt_submit",
	)
	evidence, err := model.NewHookDeliveryEvidence(event, nativeID, "ws")
	if err != nil {
		t.Fatalf("NewHookDeliveryEvidence() error = %v", err)
	}
	event.SetDeliveryEvidence(evidence)
	return event
}
