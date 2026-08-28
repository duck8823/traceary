package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func compressibleBody(tag string) string {
	return tag + " " + string(bytes.Repeat([]byte("redacted synthetic payload "), 128))
}

func openPayloadEncodeFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "payload-encode.db")
	database := NewDatabase(path, preparedMigrations(t))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(path))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		t.Fatalf("enable foreign_keys: %v", err)
	}
	return db, path
}

func closePayloadEncodeFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

func encodeAllPayloadLanes(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if err := encodeAvailableEventBodies(ctx, db); err != nil {
		t.Fatalf("encodeAvailableEventBodies: %v", err)
	}
	if _, err := encodeCommandAuditPayloadsOn(ctx, db, auditEncodePageRows); err != nil {
		t.Fatalf("encodeCommandAuditPayloadsOn: %v", err)
	}
}

type auditSeed struct {
	EventID             string
	Command             string
	Input               string
	Output              string
	InputOriginalBytes  int64
	OutputOriginalBytes int64
}

func insertPlaintextAudit(t *testing.T, db *sql.DB, seed auditSeed) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO command_audits(
			event_id, command_text, input_text, output_text,
			input_truncated, output_truncated, input_original_bytes, output_original_bytes,
			exit_code, failed
		) VALUES (?, ?, ?, ?, 0, 0, ?, ?, 0, 0)`,
		seed.EventID, seed.Command, seed.Input, seed.Output,
		seed.InputOriginalBytes, seed.OutputOriginalBytes,
	); err != nil {
		t.Fatalf("insert plaintext audit %s: %v", seed.EventID, err)
	}
}

func reencodeAuditFieldToZstd(t *testing.T, db *sql.DB, eventID, field, plaintext string) {
	t.Helper()
	encoded, err := encodePayload([]byte(plaintext), payloadCodecZstd)
	if err != nil {
		t.Fatalf("encode zstd %s: %v", field, err)
	}
	if encoded.StoredBytes >= encoded.PlaintextBytes {
		t.Fatalf("zstd did not shrink %s (%d → %d)", field, encoded.PlaintextBytes, encoded.StoredBytes)
	}
	column := field + "_text"
	prefix := field
	if field == "command" {
		column = "command_text"
		prefix = "command"
	}
	if _, err := db.Exec(`
		UPDATE command_audits
		   SET `+column+` = ?,
		       `+prefix+`_codec = ?,
		       `+prefix+`_format_version = ?,
		       `+prefix+`_plaintext_bytes = ?,
		       `+prefix+`_encoded_bytes = ?,
		       `+prefix+`_sha256 = ?
		 WHERE event_id = ?`,
		encoded.Bytes, encoded.Codec, encoded.FormatVersion,
		encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256, eventID,
	); err != nil {
		t.Fatalf("re-encode audit %s to zstd: %v", field, err)
	}
}

func assertBodyCodec(t *testing.T, db *sql.DB, id, want string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRow(`SELECT body_codec FROM events WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("read body_codec %s: %v", id, err)
	}
	if !got.Valid {
		t.Fatalf("body_codec for %s is NULL, want %q", id, want)
	}
	if got.String != want {
		t.Fatalf("body_codec for %s = %q, want %q", id, got.String, want)
	}
}
