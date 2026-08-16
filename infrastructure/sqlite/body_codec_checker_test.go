package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCheckBodyCodec_ReportsUnknownAndIgnoresSupportedAndNull(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, preparedMigrations(t))
	store := NewStoreManagementDatasource(database)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", directSQLiteRWDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	insert := func(id, codec string, codecValid bool) {
		t.Helper()
		var bodyCodec any
		if codecValid {
			bodyCodec = codec
		}
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_codec)
			 VALUES (?, 'prompt', 'codex', 's1', 'w1', 'body', '2026-04-10T00:00:00Z', 'user_prompt_submit', 'hook', ?)`,
			id, bodyCodec,
		); err != nil {
			t.Fatalf("insert %s error = %v", id, err)
		}
	}
	insert("evt-identity", payloadCodecIdentity, true)
	insert("evt-zstd", payloadCodecZstd, true)
	insert("evt-null", "", false)
	insert("evt-gzip-1", "gzip", true)
	insert("evt-gzip-2", "gzip", true)
	insert("evt-zstd19", "zstd:19", true)

	state, err := NewBodyCodecChecker(database).CheckBodyCodec(context.Background())
	if err != nil {
		t.Fatalf("CheckBodyCodec() error = %v", err)
	}
	want := []struct {
		codec string
		count int64
		ids   []string
	}{
		{codec: "gzip", count: 2, ids: []string{"evt-gzip-1", "evt-gzip-2"}},
		{codec: "zstd:19", count: 1, ids: []string{"evt-zstd19"}},
	}
	if len(state.UnknownRows) != len(want) {
		t.Fatalf("UnknownRows = %+v, want %d groups", state.UnknownRows, len(want))
	}
	for i, row := range state.UnknownRows {
		if diff := cmp.Diff(want[i].codec, row.Codec); diff != "" {
			t.Fatalf("UnknownRows[%d].Codec mismatch (-want +got):\n%s", i, diff)
		}
		if row.Count != want[i].count {
			t.Fatalf("UnknownRows[%d].Count = %d, want %d", i, row.Count, want[i].count)
		}
		if diff := cmp.Diff(want[i].ids, row.SampleIDs); diff != "" {
			t.Fatalf("UnknownRows[%d].SampleIDs mismatch (-want +got):\n%s", i, diff)
		}
	}
}

func TestCheckBodyCodec_PassOnKnownOnly(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	database := NewDatabase(dbPath, preparedMigrations(t))
	store := NewStoreManagementDatasource(database)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", directSQLiteRWDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_codec)
		 VALUES ('evt-ok', 'prompt', 'codex', 's1', 'w1', 'body', '2026-04-10T00:00:00Z', 'user_prompt_submit', 'hook', ?)`,
		payloadCodecIdentity,
	); err != nil {
		t.Fatalf("insert error = %v", err)
	}

	state, err := NewBodyCodecChecker(database).CheckBodyCodec(context.Background())
	if err != nil {
		t.Fatalf("CheckBodyCodec() error = %v", err)
	}
	if len(state.UnknownRows) != 0 {
		t.Fatalf("UnknownRows = %+v, want empty", state.UnknownRows)
	}
}

func TestSupportedBodyCodecs_MatchesDecodePayload(t *testing.T) {
	t.Parallel()
	for _, codec := range SupportedBodyCodecs() {
		encoded, err := encodePayload([]byte("hello"), codec)
		if err != nil {
			t.Fatalf("encodePayload(%q) error = %v", codec, err)
		}
		if _, err := decodePayload(encoded.Bytes, codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.SHA256, maxDecodedPayloadBytes); err != nil {
			t.Fatalf("decodePayload(%q) error = %v, want supported", codec, err)
		}
	}
	if _, err := decodePayload([]byte("hello"), "gzip", payloadFormatVersion, 5, "deadbeef", maxDecodedPayloadBytes); err == nil {
		t.Fatal("decodePayload(gzip) error = nil, want unsupported")
	}
}
