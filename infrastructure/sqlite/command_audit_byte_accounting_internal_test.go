package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// Once command_text is zstd-compressed, length(CAST(command_text AS BLOB)) is
// the physical size. StoredBytes must report the logical command length so
// handoff previews do not under-count retained history.
func TestRecentCommandPreviewsStoredBytesPreferPlaintextOnCompressedCorpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "preview-bytes.db")
	database := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	ds := NewEventDatasource(database)

	commandPlain := strings.Repeat("preview-command-payload-", 256)
	event := model.EventOf(
		mustEventID(t, "event-compressed-cmd"), types.EventKindCommandExecuted,
		types.Client("cli"), mustAgent(t, "codex"),
		mustSessionID(t, "session-preview"), types.Workspace("ws"), "",
		time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC),
	)
	if err := ds.Save(ctx, event); err != nil {
		t.Fatalf("Save: %v", err)
	}

	db, err := sql.Open("sqlite", directSQLiteRWDSN(path))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`
		INSERT INTO command_audits(
			event_id, command_text, input_text, output_text,
			input_truncated, output_truncated, input_original_bytes, output_original_bytes,
			exit_code, failed
		) VALUES (?, ?, '', '', 0, 0, 0, 0, 0, 0)`, event.EventID().String(), commandPlain); err != nil {
		t.Fatalf("insert audit: %v", err)
	}

	const previewRunes = 64
	got, err := ds.ListRecentCommandPreviews(ctx, types.SessionID("session-preview"), 10, previewRunes)
	if err != nil {
		t.Fatalf("ListRecentCommandPreviews: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("previews = %d, want 1", len(got))
	}
	if got[0].StoredBytes() != len(commandPlain) {
		t.Fatalf("stored bytes = %d, want plaintext %d", got[0].StoredBytes(), len(commandPlain))
	}
	wantBody := string([]rune(commandPlain)[:previewRunes])
	if diff := cmp.Diff(wantBody, got[0].Body()); diff != "" {
		t.Fatalf("preview body mismatch (-want +got):\n%s", diff)
	}
}

// Audit source sizing uses a physical stored figure (intentional length()
// terms) and a logical decoded figure that prefers *_plaintext_bytes. Against a
// compressed audit corpus the two must diverge and the logical figure must
// match the pre-codec plaintext sizes.
func TestAuditSourceSizingMatchesStoredPlaintext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit-size.db")
	database := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(path))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	const id = "proj-size"
	command := compressibleBody("proj-cmd")
	input := compressibleBody("proj-in")
	output := compressibleBody("proj-out")
	if _, err := db.Exec(`
INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at)
VALUES(?, 'command_executed', 'cli', 'codex', 'session', 'ws', '', ?)`,
		id, time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed)
VALUES(?, ?, ?, ?, 0, 0, 0, 0, 0, 0)`, id, command, input, output); err != nil {
		t.Fatalf("insert audit: %v", err)
	}

	var stored int64
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(length(CAST(e.body AS BLOB)),0)
		     + COALESCE(length(CAST(a.command_text AS BLOB)),0)
		     + COALESCE(length(CAST(a.input_text AS BLOB)),0)
		     + COALESCE(length(CAST(a.output_text AS BLOB)),0)
		  FROM events e
		  LEFT JOIN command_audits a ON a.event_id = e.id
		 WHERE e.id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("size query: %v", err)
	}
	want := int64(len(command) + len(input) + len(output))
	if diff := cmp.Diff(want, stored); diff != "" {
		t.Fatalf("stored bytes mismatch (-want +got):\n%s", diff)
	}
}

func mustEventID(t *testing.T, value string) types.EventID {
	t.Helper()
	id, err := types.EventIDFrom(value)
	if err != nil {
		t.Fatalf("EventIDFrom(%q): %v", value, err)
	}
	return id
}

func mustAgent(t *testing.T, value string) types.Agent {
	t.Helper()
	agent, err := types.AgentFrom(value)
	if err != nil {
		t.Fatalf("AgentFrom(%q): %v", value, err)
	}
	return agent
}

func mustSessionID(t *testing.T, value string) types.SessionID {
	t.Helper()
	return types.SessionID(value)
}
