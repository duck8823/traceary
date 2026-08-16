package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A decode failure that is not a *PayloadIntegrityError means the store is not
// in the shape the scan assumes, so it must still abort the whole run rather
// than being classified as a per-row skip.
func TestDedupeUnreadableBodyReason_NonIntegrityErrorAborts(t *testing.T) {
	t.Parallel()

	_, tolerable := dedupeUnreadableBodyReason(errors.New("boom"))
	if tolerable {
		t.Fatal("tolerable = true for a non-integrity decode error, want false")
	}
}

// An oversized stored payload is classified as a tolerable integrity error,
// not silently truncated, and its detail names the codec and the reason
// without ever including payload content.
func TestDedupeUnreadableBodyReason_OversizedStoredBodyIsTolerable(t *testing.T) {
	t.Parallel()

	payload := payloadRow{Codec: sql.NullString{String: "zstd", Valid: true}}
	storedLength := sql.NullInt64{Int64: maxStoredPayloadBytes + 1, Valid: true}
	_, err := decodeDedupeCandidateBody(payload, storedLength, true, "evt-oversized")
	if err == nil {
		t.Fatal("decodeDedupeCandidateBody() error = nil, want an oversized-length error")
	}

	detail, tolerable := dedupeUnreadableBodyReason(err)
	if !tolerable {
		t.Fatal("tolerable = false, want true for an oversized stored body")
	}
	if !strings.Contains(detail, "codec=zstd") || !strings.Contains(detail, "stored length exceeds limit") {
		t.Fatalf("detail = %q, want it to name the codec and the reason", detail)
	}
}

// permittedDedupeDrops is what compaction's deleteNonCanonicalDuplicateEvents
// relies on to decide which duplicates it may drop. A row whose body cannot be
// decoded has no identity, so it must never appear as a drop candidate, and its
// presence must not stop an otherwise-decodable duplicate pair from being
// planned.
func TestPermittedDedupeDrops_PreservesUnreadableBody(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	rows := []struct{ id, body, createdAt string }{
		{"evt-p1", "hello", "2026-05-01T00:00:00Z"},
		{"evt-p2", "hello", "2026-05-01T00:00:03Z"},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, 'prompt', 'codex', 's1', 'w1', ?, ?, 'user_prompt_submit', 'hook')`,
			r.id, r.body, r.createdAt,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	// evt-px: body_codec is set but the other four payload metadata columns are
	// left NULL, so payloadRow.decode reports "incomplete metadata" -- a
	// tolerable *PayloadIntegrityError.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_codec)
		 VALUES ('evt-px', 'prompt', 'codex', 's1', 'w1', 'corrupt', '2026-05-01T00:00:05Z', 'user_prompt_submit', 'hook', 'zstd')`,
	); err != nil {
		t.Fatalf("insert evt-px error = %v", err)
	}

	drops, err := permittedDedupeDrops(ctx, db)
	if err != nil {
		t.Fatalf("permittedDedupeDrops() error = %v", err)
	}
	if _, ok := drops["evt-px"]; ok {
		t.Fatal("unreadable row evt-px was included in the drop set")
	}
	if kept, ok := drops["evt-p2"]; !ok || kept != "evt-p1" {
		t.Fatalf("drops[evt-p2] = (%q, %v), want (evt-p1, true)", kept, ok)
	}
}
