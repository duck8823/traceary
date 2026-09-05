package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"testing"
)

func TestCanonicalEncodedValueFreezesTypeAndIntegerFraming(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"null", nil, "00"},
		{"negative integer", int64(-1), "01ffffffffffffffff"},
		{"text", "A", "02000000000000000141"},
		{"blob", []byte("A"), "03000000000000000141"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalEncodedValue(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(got) != tc.want {
				t.Fatalf("encoded value = %x, want %s", got, tc.want)
			}
		})
	}
	if _, err := canonicalEncodedValue(float64(1)); err == nil {
		t.Fatal("REAL canonical value was accepted")
	}
}

func TestCanonicalEventAuditDigestGolden(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/store.db"
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 35))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO events(
		id,kind,agent,session_id,body,created_at,client,workspace,source_hook,
		body_original_bytes,body_stored_bytes,body_ingest_truncated,body_storage_truncated,body_metadata_version,
		body_availability,body_pruned_at,body_pruned_plan_id,created_at_norm
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"e-1", "command", "codex", "s-1", "hello", "2026-08-03T00:00:00Z", "cli", "/repo", nil,
		5, 5, 0, 0, 1, "available", nil, nil, "2026-08-03T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO command_audits(
		event_id,command_text,input_text,output_text,input_truncated,output_truncated,exit_code,failed,
		input_original_bytes,output_original_bytes,command_wrapper,command_name,failure_reason
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "e-1", "echo hi", "", "hi", 0, 0, -1, 1, 0, 2, "direct", "echo", "exit_code"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	immutable, err := openDirectReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = immutable.Close() }()
	evidence, err := CanonicalEventAuditDigest(ctx, immutable)
	if err != nil {
		t.Fatal(err)
	}
	const want = "abc07538b9381f0004fbf8e9b9aab10906441146092ab7fd1f63398f5ba68f10"
	if evidence.EventCount != 1 || evidence.AuditCount != 1 || evidence.Digest != want {
		t.Fatalf("canonical evidence = %+v; update golden %q", evidence, evidence.Digest)
	}
}

func TestCanonicalEventAuditDigestEmptyGolden(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/store.db"
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrationsBefore(t, 35))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := openDirectReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	evidence, err := CanonicalEventAuditDigest(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	const want = "52a8d288e0387f97e487ddfe5c3105cd09b53f952e6154986710de8610cffdac"
	if evidence.EventCount != 0 || evidence.AuditCount != 0 || evidence.Digest != want {
		t.Fatalf("empty canonical evidence = %+v; update golden %q", evidence, evidence.Digest)
	}
}

func TestCanonicalAccumulatorIsOrderIndependentAndMultiplicitySensitive(t *testing.T) {
	a := [32]byte{1, 2, 3, 4}
	b := [32]byte{9, 8, 7, 6}
	var left, right, duplicate canonicalAccumulator
	left.add(a)
	left.add(b)
	right.add(b)
	right.add(a)
	duplicate.add(a)
	duplicate.add(b)
	duplicate.add(b)
	if left.tableDigest("events", canonicalEventColumns) != right.tableDigest("events", canonicalEventColumns) {
		t.Fatal("canonical accumulator depends on row delivery order")
	}
	if left.tableDigest("events", canonicalEventColumns) == duplicate.tableDigest("events", canonicalEventColumns) {
		t.Fatal("canonical accumulator ignored multiplicity")
	}
}

func TestCanonicalPayloadNormalizesIdentityAndZstdAndFailsClosed(t *testing.T) {
	plaintext := []byte("same logical payload")
	encoded, err := encodePayload(plaintext, payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := canonicalPayload(encoded.Bytes, []any{
		encoded.Codec, int64(encoded.FormatVersion), encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := canonicalPayload(string(plaintext), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(legacy) {
		t.Fatalf("decoded = %q, legacy = %q", decoded, legacy)
	}
	if _, err = canonicalPayload(encoded.Bytes, []any{encoded.Codec, nil, encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256}); err == nil {
		t.Fatal("partial codec metadata was accepted")
	}
	binaryPayload := []byte{0xff}
	decodedBinary, err := canonicalPayload(binaryPayload, nil)
	if err != nil {
		t.Fatalf("binary payload error = %v", err)
	}
	if string(decodedBinary) != string(binaryPayload) {
		t.Fatalf("binary payload = %x, want %x", decodedBinary, binaryPayload)
	}
	got, err := canonicalEncodedValue(canonicalLogicalValue(binaryPayload))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != "030000000000000001ff" {
		t.Fatalf("binary logical value encoded = %x, want blob tag", got)
	}
}
