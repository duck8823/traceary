package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	domtypes "github.com/duck8823/traceary/domain/types"
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

func TestPayloadRowHydratesLegacyIdentityAndZstdWithoutReservedPrefix(t *testing.T) {
	legacy := payloadRow{Stored: []byte("traceary-payload-v1:{legitimate plaintext}")}
	got, err := legacy.decode(maxDecodedPayloadBytes)
	if err != nil || string(got) != string(legacy.Stored) {
		t.Fatalf("legacy = %q, %v", got, err)
	}
	encoded, err := encodePayload([]byte("zstd shadow row"), payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	row := payloadRow{Stored: encoded.Bytes,
		Codec:          sql.NullString{String: encoded.Codec, Valid: true},
		FormatVersion:  sql.NullInt64{Int64: int64(encoded.FormatVersion), Valid: true},
		PlaintextBytes: sql.NullInt64{Int64: encoded.PlaintextBytes, Valid: true},
		StoredBytes:    sql.NullInt64{Int64: encoded.StoredBytes, Valid: true},
		SHA256:         sql.NullString{String: encoded.SHA256, Valid: true}}
	got, err = row.decode(maxDecodedPayloadBytes)
	if err != nil || string(got) != "zstd shadow row" {
		t.Fatalf("zstd = %q, %v", got, err)
	}
	row.StoredBytes.Int64++
	if _, err := row.decode(maxDecodedPayloadBytes); !isPayloadIntegrityError(err) {
		t.Fatalf("corrupt error = %v", err)
	}
}

func TestPayloadRowRejectsEveryPartialMetadataCombination(t *testing.T) {
	fields := []func(*payloadRow){
		func(row *payloadRow) { row.Codec = sql.NullString{String: payloadCodecIdentity, Valid: true} },
		func(row *payloadRow) { row.FormatVersion = sql.NullInt64{Int64: 1, Valid: true} },
		func(row *payloadRow) { row.PlaintextBytes = sql.NullInt64{Int64: 1, Valid: true} },
		func(row *payloadRow) { row.StoredBytes = sql.NullInt64{Int64: 1, Valid: true} },
		func(row *payloadRow) { row.SHA256 = sql.NullString{String: strings.Repeat("0", 64), Valid: true} },
	}
	for index, set := range fields {
		row := payloadRow{Stored: []byte("x")}
		set(&row)
		_, err := row.decode(maxDecodedPayloadBytes)
		var integrity *PayloadIntegrityError
		if !errors.As(err, &integrity) || integrity.Reason != "incomplete metadata" {
			t.Fatalf("partial metadata %d error = %v", index, err)
		}
	}
}

func TestLoadEventPlaintextRejectsOversizedPhysicalValueBeforeScan(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/oversized.db"
	database := NewDatabase(path, os.DirFS("../../schema/sqlite/migrations"))
	if err := database.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for _, codec := range []string{"legacy", payloadCodecIdentity, payloadCodecZstd} {
		id := "oversized-" + codec
		metadata := []any{nil, nil, nil, nil, nil}
		if codec != "legacy" {
			metadata = []any{codec, 1, maxDecodedPayloadBytes + 1, maxDecodedPayloadBytes + 1, strings.Repeat("0", 64)}
		}
		_, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,source_hook,
body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256)
VALUES(?, 'prompt','hook','codex',?,'/repo',zeroblob(?),'2026-08-01T00:00:00Z','user_prompt_submit',?,?,?,?,?)`,
			id, id, maxDecodedPayloadBytes+1, metadata[0], metadata[1], metadata[2], metadata[3], metadata[4])
		if err != nil {
			t.Fatal(err)
		}
		_, err = loadEventPlaintext(ctx, raw, id)
		var integrity *PayloadIntegrityError
		if !errors.As(err, &integrity) || integrity.RowID != id || integrity.Field != "body" || integrity.Reason != "stored length exceeds limit" {
			t.Fatalf("%s error = %#v", codec, err)
		}
	}
}

func TestMigrationDoesNotInstallApplicationFunctionDependentPayloadTrigger(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/external-writer.db"
	database := NewDatabase(path, os.DirFS("../../schema/sqlite/migrations"))
	if err := database.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var count int
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='events_identity_payload_metadata_after_update'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("application-dependent triggers = %d", count)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,source_hook) VALUES('external','prompt','legacy','legacy','s','/repo','before','2026-08-01T00:00:00Z','user_prompt_submit')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE events SET body='after' WHERE id='external'`); err != nil {
		t.Fatalf("external SQLite update failed: %v", err)
	}
}

func TestProductionQueriesHydrateMixedRowsAndMetadataSurvivesCorruption(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/mixed.db"
	database := NewDatabase(path, os.DirFS("../../schema/sqlite/migrations"))
	if err := database.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	insert := func(id, session, plaintext, codec string, corrupt bool) {
		t.Helper()
		payload, err := encodePayload([]byte(plaintext), codec)
		if err != nil {
			t.Fatal(err)
		}
		checksum := payload.SHA256
		if corrupt {
			checksum = strings.Repeat("0", 64)
		}
		_, err = raw.ExecContext(ctx, `INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at,source_hook,
body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256)
VALUES(?, 'prompt','hook','codex',?,'/repo',?,'2026-08-01T00:00:00Z','user_prompt_submit',?,?,?,?,?)`,
			id, session, payload.Bytes, payload.Codec, payload.FormatVersion, payload.PlaintextBytes, payload.StoredBytes, checksum)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("identity", "identity-session", "identity body", payloadCodecIdentity, false)
	insert("zstd", "zstd-session", "compressed body", payloadCodecZstd, false)
	insert("timeline-zstd", "tree-root", "timeline compressed prompt", payloadCodecZstd, false)
	if _, err := raw.ExecContext(ctx, `UPDATE events SET workspace='/timeline', created_at='2026-08-01T01:00:00Z' WHERE id='timeline-zstd'`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO sessions(session_id,started_at,client,agent,workspace,label,summary) VALUES('tree-root','2026-08-01T00:00:00Z','hook','codex','/timeline','','')`); err != nil {
		t.Fatal(err)
	}
	command, _ := encodePayload([]byte("echo redacted"), payloadCodecZstd)
	input, _ := encodePayload([]byte("input"), payloadCodecIdentity)
	output, _ := encodePayload([]byte("output"), payloadCodecZstd)
	_, err = raw.ExecContext(ctx, `INSERT INTO command_audits(event_id,command_text,command_wrapper,command_name,input_text,output_text,input_truncated,output_truncated,input_original_bytes,output_original_bytes,failed,failure_reason,
command_codec,command_format_version,command_plaintext_bytes,command_encoded_bytes,command_sha256,
input_codec,input_format_version,input_plaintext_bytes,input_encoded_bytes,input_sha256,
output_codec,output_format_version,output_plaintext_bytes,output_encoded_bytes,output_sha256)
VALUES('zstd',?,'','echo',?,?,0,0,5,6,0,'unknown',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		command.Bytes, input.Bytes, output.Bytes,
		command.Codec, command.FormatVersion, command.PlaintextBytes, command.StoredBytes, command.SHA256,
		input.Codec, input.FormatVersion, input.PlaintextBytes, input.StoredBytes, input.SHA256,
		output.Codec, output.FormatVersion, output.PlaintextBytes, output.StoredBytes, output.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	insert("corrupt", "corrupt-session", "corrupt body", payloadCodecZstd, true)
	if _, err := raw.ExecContext(ctx, `UPDATE events SET created_at='2030-01-01T00:00:00Z' WHERE id='corrupt'`); err != nil {
		t.Fatal(err)
	}
	datasource := NewEventDatasource(database)
	eventUC := usecase.NewEventUsecase(datasource, datasource)
	redacted, err := eventUC.Log(ctx, "Authorization: Bearer secret-token-value", domtypes.EventKindTranscript, "hook", "codex", "redaction-session", "/repo", apptypes.NewLogRedactionBuilder().Build())
	if err != nil {
		t.Fatal(err)
	}
	var storedBody, checksum string
	if err := raw.QueryRowContext(ctx, `SELECT CAST(body AS TEXT), body_sha256 FROM events WHERE id=?`, redacted.EventID().String()).Scan(&storedBody, &checksum); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedBody, "secret-token-value") || !strings.Contains(storedBody, "[REDACTED]") {
		t.Fatalf("stored redaction body = %q", storedBody)
	}
	digest := sha256.Sum256([]byte(storedBody))
	if checksum != hex.EncodeToString(digest[:]) {
		t.Fatal("checksum was not computed after redaction")
	}
	timeline, err := datasource.ListTimelineBlocks(ctx, "/timeline", time.Time{}, time.Time{}, 1800, 10)
	if err != nil || len(timeline) != 1 || timeline[0].WorkspaceBreakdown()[0].Summary() != "timeline compressed prompt" {
		t.Fatalf("timeline = %#v, %v", timeline, err)
	}
	sessions := NewSessionDatasource(database)
	tree, err := sessions.ListTreeSummaries(ctx, 10, "/timeline", "tree-root")
	if err != nil || len(tree) != 1 || tree[0].LatestEventMessage() != "timeline compressed prompt" {
		t.Fatalf("tree = %#v, %v", tree, err)
	}
	got, err := datasource.ListRecent(ctx, 10, 0, "", "", "", "zstd-session", "", false, time.Time{}, time.Time{}, "")
	if err != nil || len(got) != 1 || got[0].Body() != "compressed body" {
		t.Fatalf("zstd query = %#v, %v", got, err)
	}
	// Simulate a missing FTS projection. Bounded fallback must select IDs in
	// SQL, then decode both the event and audit payloads before matching.
	if _, err := raw.ExecContext(ctx, `DELETE FROM event_search_documents WHERE event_id='zstd'`); err != nil {
		t.Fatal(err)
	}
	searched, err := datasource.Search(ctx, "compressed", "", "zstd-session", "", "", "", time.Time{}, time.Time{}, 10, 0, false)
	if err != nil || len(searched) != 1 || searched[0].Body() != "compressed body" {
		t.Fatalf("search = %#v, %v", searched, err)
	}
	shortSearched, err := datasource.Search(ctx, "ed", "", "zstd-session", "", "", "", time.Time{}, time.Time{}, 10, 0, false)
	if err != nil || len(shortSearched) != 1 || shortSearched[0].EventID().String() != "zstd" {
		t.Fatalf("short decoded search = %#v, %v", shortSearched, err)
	}
	auditSearched, err := datasource.Search(ctx, "redacted", "", "zstd-session", "", "", "", time.Time{}, time.Time{}, 10, 0, false)
	if err != nil || len(auditSearched) != 1 || auditSearched[0].EventID().String() != "zstd" {
		t.Fatalf("audit decoded search = %#v, %v", auditSearched, err)
	}
	details, err := datasource.GetDetails(ctx, "zstd")
	if err != nil {
		t.Fatal(err)
	}
	audit, ok := details.CommandAudit().Value()
	if !ok || audit.Command() != "echo redacted" || audit.Output() != "output" {
		t.Fatalf("audit = %#v", audit)
	}
	_, err = datasource.ListRecent(ctx, 10, 0, "", "", "", "corrupt-session", "", false, time.Time{}, time.Time{}, "")
	var integrity *PayloadIntegrityError
	if !errors.As(err, &integrity) || integrity.RowID != "corrupt" || integrity.Field != "body" {
		t.Fatalf("corrupt error = %#v, %v", integrity, err)
	}
	metadata, err := datasource.ListRecentMetadata(ctx, apptypes.NewEventListCriteriaBuilder(10).Build())
	if err != nil || len(metadata) != 5 {
		t.Fatalf("metadata = %d, %v", len(metadata), err)
	}
	bundle := NewBundleDatasource(database, datasource)
	audits, err := bundle.ListBundleCommandAudits(ctx)
	if err != nil || len(audits) != 1 || audits[0].Output() != "output" {
		t.Fatalf("bundle audits = %#v, %v", audits, err)
	}
	manager := NewStoreManagementDatasource(database)
	tables, err := manager.ListArchiveEligible(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetEvents)
	if err != nil {
		t.Fatal(err)
	}
	foundZstd := false
	for _, table := range tables {
		if table.Name == "events" {
			for _, row := range table.Rows {
				if row["id"] == "zstd" && row["body"] == "compressed body" {
					foundZstd = true
				}
			}
		}
	}
	if !foundZstd {
		t.Fatal("archive export did not expose decoded zstd body")
	}
	if _, err := manager.DeleteArchiveRows(ctx, map[string][]string{"events": {"identity", "zstd"}}); err != nil {
		t.Fatal(err)
	}
	inserted, _, _, err := manager.RestoreArchiveRows(ctx, tables, false)
	if err != nil || inserted < 3 {
		t.Fatalf("archive restore = %d, %v", inserted, err)
	}
	restored, err := datasource.GetDetails(ctx, "zstd")
	if err != nil || restored.Event().Body() != "compressed body" {
		t.Fatalf("restored = %#v, %v", restored, err)
	}
}

func TestPayloadDecodeBoundaryAndHighRatioBomb(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), int(maxDecodedPayloadBytes))
	encoded, err := encodePayload(payload, payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePayload(encoded.Bytes, encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.SHA256, maxDecodedPayloadBytes); err != nil {
		t.Fatalf("exact boundary: %v", err)
	}
	if _, err := decodePayload(encoded.Bytes, encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.SHA256, maxDecodedPayloadBytes-1); !isPayloadIntegrityError(err) {
		t.Fatalf("limit-1 error = %v", err)
	}
	bomb := encoded
	bomb.PlaintextBytes = maxDecodedPayloadBytes + 1
	if _, err := decodePayload(bomb.Bytes, bomb.Codec, bomb.FormatVersion, bomb.PlaintextBytes, bomb.SHA256, maxDecodedPayloadBytes); !isPayloadIntegrityError(err) {
		t.Fatalf("bomb error = %v", err)
	}
}

func TestStoreCompatibilityRejectsMissingStateRow(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE store_format_state(singleton INTEGER PRIMARY KEY, minimum_reader_version INTEGER NOT NULL, maximum_payload_format INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStoreCompatibility(context.Background(), db); err == nil {
		t.Fatal("missing singleton state was accepted")
	}
}
