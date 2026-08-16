package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// dedupeCodecMigrations returns the on-disk embedded migration set, matching
// the pattern content_event_dedupe_unreadable_internal_test.go already uses
// for internal (package sqlite) tests that need an on-disk store.
func dedupeCodecMigrations() fs.FS {
	return os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations"))
}

// migrationsBeforeDedupeArchiveCodec returns every embedded migration older
// than #1744's, so a test can build a genuine pre-migration archive row
// before reopening with the full set -- mirrors
// migrationsBeforeCodecFoundation in payload_codec_compatibility_internal_test.go.
func migrationsBeforeDedupeArchiveCodec(t *testing.T) fstest.MapFS {
	t.Helper()
	dir := filepath.Join("..", "..", "schema", "sqlite", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "000067" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		out[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return out
}

// insertCompressedDedupeEvent writes an events row exactly as the canonical
// write path would after choosing zstd: the raw INSERT bypasses
// appendAttestationLink (unlike EventDatasource.Save), which is what keeps a
// prompt-kind row eligible for dedupe in this fixture.
func insertCompressedDedupeEvent(t *testing.T, db *sql.DB, id, sessionID, createdAt, plaintext string) encodedPayload {
	t.Helper()
	payload, err := encodePayload([]byte(plaintext), payloadCodecZstd)
	if err != nil {
		t.Fatalf("encodePayload(%s) error = %v", id, err)
	}
	if payload.StoredBytes >= payload.PlaintextBytes {
		t.Fatalf("insertCompressedDedupeEvent(%s): fixture body did not compress (stored=%d plaintext=%d)", id, payload.StoredBytes, payload.PlaintextBytes)
	}
	if _, err := db.Exec(
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client,
		                     body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256)
		 VALUES (?, 'prompt', 'codex', ?, 'w1', ?, ?, 'user_prompt_submit', 'hook', ?, ?, ?, ?, ?)`,
		id, sessionID, payload.Bytes, createdAt,
		payload.Codec, payload.FormatVersion, payload.PlaintextBytes, payload.StoredBytes, payload.SHA256,
	); err != nil {
		t.Fatalf("insert compressed event %s: %v", id, err)
	}
	return payload
}

// TestApplyDedupe_CopiesEncodedPayloadAsIs is the #1744 apply-side contract:
// quarantining a compressed duplicate must not decode it. The archived row's
// body and codec metadata must equal the source row's stored (encoded) form
// byte-for-byte, not the larger decoded plaintext.
func TestApplyDedupe_CopiesEncodedPayloadAsIs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, dedupeCodecMigrations())
	store := NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	plain := compressibleBody("apply-as-is")
	insertCompressedDedupeEvent(t, db, "evt-keep", "s1", "2026-05-01T00:00:00Z", plain)
	dupPayload := insertCompressedDedupeEvent(t, db, "evt-dup", "s1", "2026-05-01T00:00:03Z", plain)

	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	result, err := store.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "run-codec", Now: now,
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}
	if result.MovedCount() != 1 {
		t.Fatalf("MovedCount() = %d, want 1 (result=%+v)", result.MovedCount(), result)
	}

	var (
		archivedBody           []byte
		codec, sha256Hex       string
		formatVersion          int
		plaintextBytes, stored int64
	)
	if err := db.QueryRow(
		`SELECT body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		   FROM event_content_dedupe_archive WHERE id = 'evt-dup'`,
	).Scan(&archivedBody, &codec, &formatVersion, &plaintextBytes, &stored, &sha256Hex); err != nil {
		t.Fatalf("read archived row: %v", err)
	}

	if string(archivedBody) != string(dupPayload.Bytes) {
		t.Fatalf("archived body = %d bytes, want the %d encoded bytes copied verbatim (not decoded)", len(archivedBody), len(dupPayload.Bytes))
	}
	if codec != payloadCodecZstd {
		t.Fatalf("archived body_codec = %q, want %q", codec, payloadCodecZstd)
	}
	if int64(formatVersion) != int64(dupPayload.FormatVersion) || plaintextBytes != dupPayload.PlaintextBytes ||
		stored != dupPayload.StoredBytes || sha256Hex != dupPayload.SHA256 {
		t.Fatalf("archived codec metadata mismatch: got (fv=%d plain=%d stored=%d sha=%s), want (fv=%d plain=%d stored=%d sha=%s)",
			formatVersion, plaintextBytes, stored, sha256Hex,
			dupPayload.FormatVersion, dupPayload.PlaintextBytes, dupPayload.StoredBytes, dupPayload.SHA256)
	}
	if stored >= plaintextBytes {
		t.Fatalf("archived stored size (%d) is not smaller than plaintext size (%d); fixture no longer proves the archive avoids inflation", stored, plaintextBytes)
	}
}

// TestPurgeDedupeRun_ReleasedBodyReportsStoredNotDecodedSize is the
// ReleasedBody-side contract of #1744: purging a run must report the bytes a
// purge actually frees (the encoded/stored size), not the larger logical
// plaintext size a decode-on-archive design would have reported.
func TestPurgeDedupeRun_ReleasedBodyReportsStoredNotDecodedSize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, dedupeCodecMigrations())
	store := NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	plain := compressibleBody("purge-released-body")
	insertCompressedDedupeEvent(t, db, "evt-keep", "s2", "2026-05-02T00:00:00Z", plain)
	dupPayload := insertCompressedDedupeEvent(t, db, "evt-dup", "s2", "2026-05-02T00:00:03Z", plain)

	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	if _, err := store.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "run-purge", Now: now,
	}); err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}

	purge, err := store.PurgeContentEventDedupeRun(ctx, "run-purge")
	if err != nil {
		t.Fatalf("PurgeContentEventDedupeRun() error = %v", err)
	}
	if purge.ReleasedBody != dupPayload.StoredBytes {
		t.Fatalf("ReleasedBody = %d, want the stored size %d (not the decoded plaintext size %d)",
			purge.ReleasedBody, dupPayload.StoredBytes, dupPayload.PlaintextBytes)
	}
}

// TestMigration_EventContentDedupeArchiveCodecColumnsAreAdditive: a pre-#1744
// archive row (no codec columns yet) survives the new migration unchanged and
// reads back with NULL codec metadata -- the identity path, matching what
// payloadRow.decode already does for a legacy events row.
func TestMigration_EventContentDedupeArchiveCodecColumnsAreAdditive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")

	store := NewStoreManagementDatasource(NewDatabase(dbPath, migrationsBeforeDedupeArchiveCodec(t)))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize(pre-67) error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO event_content_dedupe_archive
		    (id, kind, client, agent, session_id, workspace, body, created_at,
		     source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
		 VALUES ('legacy-1', 'prompt', 'hook', 'codex', 's1', 'w1', 'legacy plaintext', '2026-01-01T00:00:00Z',
		         'user_prompt_submit', 'evt-kept', 'legacy-run', '2026-01-02T00:00:00Z', 'legacy-group', 'strict exact duplicate')`,
	); err != nil {
		t.Fatalf("insert legacy archive row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store = NewStoreManagementDatasource(NewDatabase(dbPath, dedupeCodecMigrations()))
	if err := store.InitializeAuthorized(ctx); err != nil {
		t.Fatalf("Initialize(current) error = %v", err)
	}

	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var (
		body  string
		codec sql.NullString
	)
	if err := db.QueryRow(`SELECT body, body_codec FROM event_content_dedupe_archive WHERE id = 'legacy-1'`).Scan(&body, &codec); err != nil {
		t.Fatalf("read migrated legacy row: %v", err)
	}
	if body != "legacy plaintext" {
		t.Fatalf("legacy body = %q, want unchanged", body)
	}
	if codec.Valid {
		t.Fatalf("legacy body_codec = %q, want NULL (identity by convention)", codec.String)
	}
}
