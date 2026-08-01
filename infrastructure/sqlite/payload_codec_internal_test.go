package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestPayloadCodecRoundTripsIdentityAndZstd(t *testing.T) {
	plaintext := bytes.Repeat([]byte("redacted synthetic payload "), 1024)
	for _, codec := range []string{payloadCodecIdentity, payloadCodecZstd} {
		t.Run(codec, func(t *testing.T) {
			encoded, err := encodePayload(plaintext, codec)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodePayload(encoded.Bytes, encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.SHA256, maxDecodedPayloadBytes)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded, plaintext) {
				t.Fatal("round trip changed bytes")
			}
			if encoded.StoredBytes != int64(len(encoded.Bytes)) {
				t.Fatal("stored length mismatch")
			}
		})
	}
}

func TestPayloadCodecRejectsUnknownCorruptAndOverLimitRows(t *testing.T) {
	encoded, err := encodePayload(bytes.Repeat([]byte("a"), 4096), payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name          string
		codec         string
		version       int
		bytes         []byte
		length, limit int64
		checksum      string
	}{
		{"unknown codec", "future", 1, encoded.Bytes, encoded.PlaintextBytes, maxDecodedPayloadBytes, encoded.SHA256},
		{"unknown format", payloadCodecZstd, 2, encoded.Bytes, encoded.PlaintextBytes, maxDecodedPayloadBytes, encoded.SHA256},
		{"corrupt stream", payloadCodecZstd, 1, []byte("not-zstd"), encoded.PlaintextBytes, maxDecodedPayloadBytes, encoded.SHA256},
		{"checksum", payloadCodecZstd, 1, encoded.Bytes, encoded.PlaintextBytes, maxDecodedPayloadBytes, strings.Repeat("0", 64)},
		{"bounded", payloadCodecZstd, 1, encoded.Bytes, encoded.PlaintextBytes, 1024, encoded.SHA256},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodePayload(tt.bytes, tt.codec, tt.version, tt.length, tt.checksum, tt.limit)
			if !isPayloadIntegrityError(err) {
				t.Fatalf("error = %v, want PayloadIntegrityError", err)
			}
		})
	}
}

func TestStoreCompatibilityGate(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		min, format int
		wantErr     bool
	}{
		{"supported", 34, 1, false}, {"future reader", 35, 1, true}, {"future format", 34, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.Exec(`CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY, minimum_reader_version INTEGER NOT NULL, maximum_payload_format INTEGER NOT NULL); INSERT INTO store_format_state VALUES(1, ?, ?)`, tc.min, tc.format); err != nil {
				t.Fatal(err)
			}
			err = checkStoreCompatibility(ctx, db)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestIdentityMetadataChecksum(t *testing.T) {
	plaintext := []byte("secret=[REDACTED]")
	encoded, err := encodePayload(plaintext, payloadCodecIdentity)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(plaintext)
	if encoded.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("checksum was not computed over redacted plaintext")
	}
	var integrity *PayloadIntegrityError
	_, err = decodePayload([]byte("secret=raw"), encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.SHA256, maxDecodedPayloadBytes)
	if !errors.As(err, &integrity) {
		t.Fatalf("error = %v", err)
	}
}

func BenchmarkPayloadCodecSynthetic(b *testing.B) {
	payload := bytes.Repeat([]byte(`{"type":"text","text":"synthetic redacted trace payload"}`), 16384)
	b.Run("identity-write", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := encodePayload(payload, payloadCodecIdentity); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("zstd-write", func(b *testing.B) {
		probe, err := encodePayload(payload, payloadCodecZstd)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(probe.StoredBytes)/float64(probe.PlaintextBytes), "stored/plain")
		for i := 0; i < b.N; i++ {
			if _, err := encodePayload(payload, payloadCodecZstd); err != nil {
				b.Fatal(err)
			}
		}
	})
	encoded, err := encodePayload(payload, payloadCodecZstd)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("maximum-bounded-decode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := decodePayload(encoded.Bytes, encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.SHA256, int64(len(payload))); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestDatabaseOpenModesEnforceCompatibility(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/future.db"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY, minimum_reader_version INTEGER NOT NULL, maximum_payload_format INTEGER NOT NULL); INSERT INTO store_format_state VALUES(1, 35, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	database := NewDatabase(path, nil)
	if _, err := database.open(ctx); err == nil {
		t.Fatal("writable open accepted future reader requirement")
	}
	if _, err := database.openReadOnly(ctx); err == nil {
		t.Fatal("read-only open accepted future reader requirement")
	}
	if _, err := NewImmutableReadDatabase(ctx, path); err == nil {
		t.Fatal("immutable open accepted future reader requirement")
	}
}

func TestMixedPayloadRowsKeepHealthyRowsAndMetadataAvailable(t *testing.T) {
	identity, err := encodePayload([]byte("identity row"), payloadCodecIdentity)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := encodePayload([]byte("compressed row"), payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := compressed
	corrupt.SHA256 = strings.Repeat("f", 64)

	// Metadata is read before individual hydration, so one row-level error
	// cannot erase the status of another row.
	rows := []struct {
		id, status string
		payload    encodedPayload
	}{{"one", "available", identity}, {"two", "available", corrupt}, {"three", "available", compressed}}
	statuses := map[string]string{}
	decoded := map[string]string{}
	failures := 0
	for _, row := range rows {
		statuses[row.id] = row.status
		plain, err := decodePayload(row.payload.Bytes, row.payload.Codec, row.payload.FormatVersion, row.payload.PlaintextBytes, row.payload.SHA256, maxDecodedPayloadBytes)
		if err != nil {
			failures++
			continue
		}
		decoded[row.id] = string(plain)
	}
	if failures != 1 || len(statuses) != 3 {
		t.Fatalf("failures=%d statuses=%d", failures, len(statuses))
	}
	if decoded["one"] != "identity row" || decoded["three"] != "compressed row" {
		t.Fatalf("healthy rows = %#v", decoded)
	}
}

func TestStoredPayloadHydrationSupportsLegacyIdentityAndZstdEnvelope(t *testing.T) {
	if got, err := decodeStoredPayload("legacy identity", maxDecodedPayloadBytes); err != nil || got != "legacy identity" {
		t.Fatalf("legacy = %q, %v", got, err)
	}
	encoded, err := encodePayload([]byte("zstd shadow row"), payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := marshalPayloadEnvelope(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStoredPayload(envelope, maxDecodedPayloadBytes)
	if err != nil || got != "zstd shadow row" {
		t.Fatalf("zstd = %q, %v", got, err)
	}
	if _, err := decodeStoredPayload(payloadEnvelopePrefix+"{", maxDecodedPayloadBytes); !isPayloadIntegrityError(err) {
		t.Fatalf("corrupt error = %v", err)
	}
}
