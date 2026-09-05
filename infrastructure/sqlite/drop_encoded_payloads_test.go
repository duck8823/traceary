package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	apptypes "github.com/duck8823/traceary/application/types"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestDropEncodedPayloads_LiveOpenLeavesPopulatedStoreUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	seedLegacyStore(t, path, 82)
	insertSourceEvent(t, path, "legacy-event")
	before := readStoreBytes(t, path)

	current := newStoreManagementDatasource(t, path, onDiskSQLiteMigrations(t))
	err := current.Initialize(ctx)
	var required *apptypes.OfflineMigrationsRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("error=%v, want OfflineMigrationsRequiredError", err)
	}
	if len(required.Versions) == 0 || required.Versions[len(required.Versions)-1] != 83 {
		t.Fatalf("pending offline = %v, want suffix 83", required.Versions)
	}
	after := readStoreBytes(t, path)
	if string(after) != string(before) {
		t.Fatal("live open mutated the populated store")
	}
}

func TestDropEncodedPayloads_MixedCodecUpgrade(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 82)
	identityBody := "identity body"
	zstdBody := strings.Repeat("zstd body should compress ", 64)
	insertEncodedEvent(t, target, "e-identity", identityBody, "identity")
	insertEncodedEvent(t, target, "e-null", "null-meta body", "")
	insertEncodedEvent(t, target, "e-zstd", zstdBody, "zstd")
	insertEncodedAudit(t, target, "e-zstd", zstdBody, "zstd")

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)

	assertMinimumReaderVersion(t, target, 39)
	assertQuickCheckOK(t, target)
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	assertNoCodecColumns(t, db, "events")
	assertNoCodecColumns(t, db, "command_audits")
	for _, table := range []string{"payload_rehearsal_runs", "payload_rehearsal_rows", "payload_rehearsal_checkpoints", "payload_backfill_runs", "payload_codec_compatibility_state"} {
		if tablePresent(t, target, table) {
			t.Fatalf("%s survived 082", table)
		}
	}
	assertEventBody(t, target, "e-identity", identityBody)
	assertEventBody(t, target, "e-null", "null-meta body")
	assertEventBody(t, target, "e-zstd", zstdBody)
	assertAuditCommand(t, target, "e-zstd", zstdBody)
	assertSurvivingMaintainers(t, db)
}

func TestDropEncodedPayloads_CorruptRowRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 82)
	insertEncodedEvent(t, target, "e-keep", "keep", "identity")
	insertCorruptEncodedEvent(t, target, "e-bad")
	beforeCount := eventCount(t, target)
	beforeDigest := eventsRowDigest(t, target)
	beforeBytes := readStoreBytes(t, target)

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	err = runUpgradeExpectError(ctx, t, dir, target, all)
	var refused *apptypes.PayloadDecodeRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want PayloadDecodeRefusedError", err)
	}
	if refused.Lane != "body" || refused.RowID != "e-bad" || refused.Reason == "" {
		t.Fatalf("refused = %+v", refused)
	}
	if eventCount(t, target) != beforeCount {
		t.Fatal("refusal changed source events count")
	}
	if eventsRowDigest(t, target) != beforeDigest {
		t.Fatal("refusal changed source events digest")
	}
	if string(readStoreBytes(t, target)) != string(beforeBytes) {
		t.Fatal("refusal mutated source file bytes")
	}
}

func TestDropEncodedPayloads_ChainedRestoreIsPlaintext(t *testing.T) {
	if testing.Short() {
		t.Skip("candidate upgrade")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory lease unsupported")
	}
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "store.db")
	seedLegacyStore(t, target, 81)
	insertSourceEvent(t, target, "e-keep")
	insertArchiveZstdRow(t, target, "arch-zstd", "zstd archived body")

	all, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	runUpgradeOn(ctx, t, dir, target, all)
	assertEventBody(t, target, "arch-zstd", "zstd archived body")
	assertMinimumReaderVersion(t, target, 39)
}

func insertEncodedEvent(t *testing.T, path, id, plaintext, codec string) {
	t.Helper()
	stored := []byte(plaintext)
	sum := sha256.Sum256([]byte(plaintext))
	format := 1
	storedBytes := len(stored)
	checksum := hex.EncodeToString(sum[:])
	if codec == "zstd" {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		stored = enc.EncodeAll([]byte(plaintext), nil)
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		storedBytes = len(stored)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if codec == "" {
		_, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace)
VALUES(?,'prompt','codex','s',?,?,'hook','w')`, id, plaintext, now)
	} else {
		_, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace,
body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256)
VALUES(?,'prompt','codex','s',?,?,'hook','w',?,?,?,?,?)`,
			id, stored, now, codec, format, len(plaintext), storedBytes, checksum)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func insertEncodedAudit(t *testing.T, path, eventID, plaintext, codec string) {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	stored := enc.EncodeAll([]byte(plaintext), nil)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(plaintext))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text,
command_codec,command_format_version,command_plaintext_bytes,command_encoded_bytes,command_sha256)
VALUES(?,?,?,?,?,?,?,?,?)`,
		eventID, stored, "", "", codec, 1, len(plaintext), len(stored), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
}

func insertCorruptEncodedEvent(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace,
body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256)
VALUES(?,'prompt','codex','s',?,?,'hook','w','zstd',1,12,3,'deadbeef')`, id, []byte("not"), now)
	if err != nil {
		t.Fatal(err)
	}
}

func assertEventBody(t *testing.T, path, id, want string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got string
	if err := db.QueryRow(`SELECT CAST(body AS TEXT) FROM events WHERE id=?`, id).Scan(&got); err != nil {
		t.Fatalf("read body %s: %v", id, err)
	}
	if got != want {
		t.Fatalf("body %s = %q, want %q", id, got, want)
	}
}

func assertAuditCommand(t *testing.T, path, eventID, want string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got string
	if err := db.QueryRow(`SELECT CAST(command_text AS TEXT) FROM command_audits WHERE event_id=?`, eventID).Scan(&got); err != nil {
		t.Fatalf("read command %s: %v", eventID, err)
	}
	if got != want {
		t.Fatalf("command %s = %q, want %q", eventID, got, want)
	}
}

func assertSurvivingMaintainers(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO events(id,kind,agent,session_id,body,created_at,client,workspace)
VALUES('probe-event','note','codex','probe',?,?,'cli','w')`, "probe body", now); err != nil {
		t.Fatalf("write-probe event: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO command_audits(event_id,command_text,input_text,output_text)
VALUES('probe-event','probe command','','')`); err != nil {
		t.Fatalf("write-probe audit: %v", err)
	}
	var stored int64
	if err := tx.QueryRow(`SELECT body_stored_bytes FROM event_metadata_projection WHERE id='probe-event'`).Scan(&stored); err != nil {
		t.Fatalf("projection after write-probe: %v", err)
	}
	if stored != int64(len("probe body")) {
		t.Fatalf("body_stored_bytes = %d, want %d", stored, len("probe body"))
	}
}
