package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
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
	err = sqlite.VerifyAttestationChain(ctx, db)
	if err == nil {
		t.Fatal("VerifyAttestationChain() error = nil after command_text UPDATE")
	}
	var undetermined *sqlite.AttestationChainUndeterminedError
	if errors.As(err, &undetermined) {
		t.Fatalf("VerifyAttestationChain() reported a real chain break as undetermined: %v", err)
	}
}

func TestVerifyAttestationChain_ZstdCommandMatchesPlaintextDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)

	prompt := model.EventOf(
		types.EventID("prompt-zstd"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"please run tests",
		time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save(prompt) error = %v", err)
	}

	zstdCommand := model.EventOf(
		types.EventID("command-zstd"), types.EventKindCommandExecuted,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"",
		time.Date(2026, 8, 14, 10, 0, 1, 0, time.UTC),
	)
	zstdAudit, err := model.NewCommandAudit(zstdCommand.EventID(), "go test ./...", "pkg", "ok", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(zstd) error = %v", err)
	}
	if err := events.SaveWithAudit(ctx, zstdCommand, zstdAudit); err != nil {
		t.Fatalf("SaveWithAudit(zstd) error = %v", err)
	}

	plainCommand := model.EventOf(
		types.EventID("command-plain"), types.EventKindCommandExecuted,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"",
		time.Date(2026, 8, 14, 10, 0, 2, 0, time.UTC),
	)
	plainAudit, err := model.NewCommandAudit(plainCommand.EventID(), "ls", "", "listed", false, false)
	if err != nil {
		t.Fatalf("NewCommandAudit(plain) error = %v", err)
	}
	if err := events.SaveWithAudit(ctx, plainCommand, plainAudit); err != nil {
		t.Fatalf("SaveWithAudit(plain) error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()

	ensureAttestedCodecColumns(t, db)
	rewriteAttestedPayloadZstd(t, db, "events", "id", "prompt-zstd", "body", []byte("please run tests"))
	rewriteAttestedPayloadZstd(t, db, "command_audits", "event_id", "command-zstd", "command", []byte("go test ./..."))
	rewriteAttestedPayloadZstd(t, db, "command_audits", "event_id", "command-zstd", "input", []byte("pkg"))

	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v, want nil when attested rows are zstd and the digest is of decoded plaintext", err)
	}
	assertAttestationCount(t, db, 3)
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
		Source: path,
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

func TestVerifyAttestationChain_SingleConnectionAfterSave(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path, events := newAttestationTestStore(t)
	prompt := model.EventOf(
		types.EventID("prompt-single-conn"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"single connection",
		time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, prompt); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v", err)
	}
}

func TestVerifyAttestationChain_ConcurrentAppendDoesNotFalseFail(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path, events := newAttestationTestStore(t)
	seed := model.EventOf(
		types.EventID("prompt-seed"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"seed",
		time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, seed); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	db := openAttestationDB(t, path)
	defer func() { _ = db.Close() }()

	errCh := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event := model.EventOf(
				types.EventID(fmt.Sprintf("prompt-conc-%d", i)), types.EventKindPrompt,
				types.Client("cli"), types.Agent("codex"),
				types.SessionID("session-1"), types.Workspace("ws"),
				fmt.Sprintf("body %d", i),
				time.Date(2026, 8, 14, 16, 0, i+1, 0, time.UTC),
			)
			if err := events.Save(ctx, event); err != nil {
				errCh <- err
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent verify/append error = %v", err)
	}
}

func TestVerifyAttestationChain_TransientContentionIsAbsorbed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)
	seed := model.EventOf(
		types.EventID("prompt-seed"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"seed",
		time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, seed); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	release := blockAttestationReads(t, path)
	go func() {
		time.Sleep(10 * time.Millisecond)
		release()
	}()

	db := openZeroBusyTimeoutAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	if err := sqlite.VerifyAttestationChain(ctx, db); err != nil {
		t.Fatalf("VerifyAttestationChain() error = %v, want nil once contention clears mid-retry", err)
	}
}

func TestVerifyAttestationChain_PersistentContentionIsUndetermined(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path, events := newAttestationTestStore(t)
	seed := model.EventOf(
		types.EventID("prompt-seed"), types.EventKindPrompt,
		types.Client("cli"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("ws"),
		"seed",
		time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC),
	)
	if err := events.Save(ctx, seed); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	release := blockAttestationReads(t, path)
	defer release()

	db := openZeroBusyTimeoutAttestationDB(t, path)
	defer func() { _ = db.Close() }()
	err := sqlite.VerifyAttestationChain(ctx, db)
	if err == nil {
		t.Fatal("VerifyAttestationChain() error = nil, want undetermined under persistent contention")
	}
	var undetermined *sqlite.AttestationChainUndeterminedError
	if !errors.As(err, &undetermined) {
		t.Fatalf("VerifyAttestationChain() error = %v, want AttestationChainUndeterminedError", err)
	}
	for _, chainWord := range []string{"attestation link", "attestation head", "digest"} {
		if strings.Contains(err.Error(), chainWord) {
			t.Fatalf("VerifyAttestationChain() undetermined message = %q, must not carry chain wording %q", err.Error(), chainWord)
		}
	}
}

// blockAttestationReads acquires an exclusive file lock on the store so any
// other connection is denied WAL index access (SQLITE_BUSY family) until
// release is called. A plain BEGIN EXCLUSIVE in WAL mode does not block
// readers, so this uses locking_mode=EXCLUSIVE per the design note's primary
// strategy (#1969 §5); it proved deterministic locally, so the
// journal_mode=DELETE fallback described there is not needed.
func blockAttestationReads(t *testing.T, path string) (release func()) {
	t.Helper()
	dsn := (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
		RawQuery: url.Values{
			"_pragma": []string{"journal_mode(WAL)", "locking_mode(EXCLUSIVE)"},
		}.Encode(),
	}).String()
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open blocking connection: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec(`BEGIN IMMEDIATE`); err != nil {
		_ = conn.Close()
		t.Fatalf("BEGIN IMMEDIATE on blocking connection: %v", err)
	}
	if _, err := conn.Exec(`UPDATE attestation_head SET seq = seq WHERE singleton = 1`); err != nil {
		_ = conn.Close()
		t.Fatalf("touch attestation_head to take the exclusive lock: %v", err)
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_, _ = conn.Exec(`ROLLBACK`)
		_ = conn.Close()
	}
}

// openZeroBusyTimeoutAttestationDB opens a verify handle with
// busy_timeout(0) so SQLite reports SQLITE_BUSY immediately instead of
// blocking inside the driver, letting the Go-level retry in
// verifyAttestationChainSnapshot be exercised deterministically.
func openZeroBusyTimeoutAttestationDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
		RawQuery: url.Values{
			"_pragma": []string{"journal_mode(WAL)", "busy_timeout(0)", "foreign_keys(1)"},
		}.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return db
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
	return sqlite.OpenStoreForTest(path)
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

func ensureAttestedCodecColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	columns := []struct {
		table  string
		column string
		decl   string
	}{
		{"events", "body_codec", "TEXT"},
		{"events", "body_format_version", "INTEGER"},
		{"events", "body_plaintext_bytes", "INTEGER"},
		{"events", "body_encoded_bytes", "INTEGER"},
		{"events", "body_sha256", "TEXT"},
		{"command_audits", "command_codec", "TEXT"},
		{"command_audits", "command_format_version", "INTEGER"},
		{"command_audits", "command_plaintext_bytes", "INTEGER"},
		{"command_audits", "command_encoded_bytes", "INTEGER"},
		{"command_audits", "command_sha256", "TEXT"},
		{"command_audits", "input_codec", "TEXT"},
		{"command_audits", "input_format_version", "INTEGER"},
		{"command_audits", "input_plaintext_bytes", "INTEGER"},
		{"command_audits", "input_encoded_bytes", "INTEGER"},
		{"command_audits", "input_sha256", "TEXT"},
	}
	for _, column := range columns {
		var n int
		query := fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info(%q) WHERE name = %q", column.table, column.column)
		if err := db.QueryRow(query).Scan(&n); err != nil {
			t.Fatalf("pragma_table_info(%s.%s): %v", column.table, column.column, err)
		}
		if n > 0 {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.column, column.decl)
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func rewriteAttestedPayloadZstd(t *testing.T, db *sql.DB, table, idColumn, id, field string, plaintext []byte) {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("zstd encoder: %v", err)
	}
	defer func() { _ = encoder.Close() }()
	stored := encoder.EncodeAll(plaintext, nil)
	sum := sha256.Sum256(plaintext)
	stmt := fmt.Sprintf(
		"UPDATE %s SET %s_text = ?, %s_codec = 'zstd', %s_format_version = 1, %s_plaintext_bytes = ?, %s_encoded_bytes = ?, %s_sha256 = ? WHERE %s = ?",
		table, field, field, field, field, field, field, idColumn,
	)
	if table == "events" {
		stmt = fmt.Sprintf(
			"UPDATE events SET body = ?, body_codec = 'zstd', body_format_version = 1, body_plaintext_bytes = ?, body_encoded_bytes = ?, body_sha256 = ? WHERE %s = ?",
			idColumn,
		)
	}
	if _, err := db.Exec(stmt, stored, int64(len(plaintext)), int64(len(stored)), hex.EncodeToString(sum[:]), id); err != nil {
		t.Fatalf("rewrite %s %s as zstd: %v", table, id, err)
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
