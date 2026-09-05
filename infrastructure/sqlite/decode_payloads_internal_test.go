package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeLaneRowsCheckpointsWALBetweenWritePages(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create db file: %v", err)
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatalf("disable autocheckpoint: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE events (
		id TEXT PRIMARY KEY,
		body TEXT,
		body_codec TEXT,
		body_format_version INTEGER,
		body_plaintext_bytes INTEGER,
		body_encoded_bytes INTEGER,
		body_sha256 TEXT
	)`); err != nil {
		t.Fatalf("create events: %v", err)
	}

	plain := compressibleBody("decode-checkpoint")
	encoded, err := encodePayload([]byte(plain), payloadCodecZstd)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	pages := 2
	extra := 7
	n := decodePayloadPageRows*pages + extra
	for i := 0; i < n; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO events(
			id, body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("e-%04d", i), encoded.Bytes, encoded.Codec, encoded.FormatVersion,
			encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256,
		); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	var checkpoints int
	decodeWALCheckpointHook = func() { checkpoints++ }
	t.Cleanup(func() { decodeWALCheckpointHook = nil })

	if err := decodeLaneRows(ctx, db, decodeLanes()[0]); err != nil {
		t.Fatalf("decodeLaneRows: %v", err)
	}
	wantPages := pages + 1
	if checkpoints != wantPages {
		t.Fatalf("WAL checkpoints = %d, want %d (one per write page)", checkpoints, wantPages)
	}
	if info, statErr := os.Stat(path + "-wal"); statErr == nil && info.Size() > 1<<20 {
		t.Fatalf("WAL %d bytes after decode, want truncated", info.Size())
	}

	var body string
	var codec sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT body, body_codec FROM events WHERE id = 'e-0500'`).Scan(&body, &codec); err != nil {
		t.Fatalf("read decoded row: %v", err)
	}
	if body != plain {
		t.Fatalf("decoded body %q, want plaintext", body)
	}
	if codec.Valid {
		t.Fatalf("body_codec still set: %q", codec.String)
	}
}
