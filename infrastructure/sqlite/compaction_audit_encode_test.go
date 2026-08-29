package sqlite_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestCompactEncodesNullCodecCommandAudits(t *testing.T) {
	dbPath := prepareAuditEncodeStore(t)
	const rows = 1200
	output := compressibleAuditOutput()
	outputSHA := sha256Hex([]byte(output))
	seedNullCodecAudits(t, dbPath, rows, "echo", "", output)

	result := compactAuditEncodeStore(t, dbPath)
	step, ok := result.Steps.Find(application.CompactStepAuditEncode)
	if !ok {
		t.Fatalf("steps=%v, want audit_encode", result.Steps)
	}
	if step.Rows != rows {
		t.Fatalf("audit_encode.rows=%d, want %d", step.Rows, rows)
	}
	if step.BytesReclaimed <= 0 {
		t.Fatalf("audit_encode.bytes_reclaimed=%d, want > 0", step.BytesReclaimed)
	}
	if step.Detail["rows_scanned"] != rows {
		t.Fatalf("rows_scanned=%d, want %d", step.Detail["rows_scanned"], rows)
	}

	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	var encoded, identity, blob, text int
	if err := db.QueryRow(`
		SELECT
		  SUM(CASE WHEN output_codec = 'zstd' THEN 1 ELSE 0 END),
		  SUM(CASE WHEN command_codec = 'identity' THEN 1 ELSE 0 END),
		  SUM(CASE WHEN typeof(output_text) = 'blob' THEN 1 ELSE 0 END),
		  SUM(CASE WHEN typeof(command_text) = 'text' THEN 1 ELSE 0 END)
		  FROM command_audits`).Scan(&encoded, &identity, &blob, &text); err != nil {
		t.Fatal(err)
	}
	if encoded != rows || identity != rows || blob != rows || text != rows {
		t.Fatalf("codec/typeof counts zstd=%d identity=%d blob=%d text=%d, want %d each", encoded, identity, blob, text, rows)
	}
	var plaintextBytes int64
	var gotSHA string
	if err := db.QueryRow(`SELECT output_plaintext_bytes, output_sha256 FROM command_audits WHERE event_id = 'audit-0'`).Scan(&plaintextBytes, &gotSHA); err != nil {
		t.Fatal(err)
	}
	if plaintextBytes != int64(len(output)) {
		t.Fatalf("output_plaintext_bytes=%d, want %d", plaintextBytes, len(output))
	}
	if gotSHA != outputSHA {
		t.Fatalf("output_sha256=%s, want %s", gotSHA, outputSHA)
	}
}

func TestCompactAuditEncodeSecondRunIsNoOp(t *testing.T) {
	dbPath := prepareAuditEncodeStore(t)
	seedNullCodecAudits(t, dbPath, 8, "echo", "in", compressibleAuditOutput())
	compactAuditEncodeStore(t, dbPath)

	db := openRetentionDB(t, dbPath)
	var beforeLen int64
	var beforeSHA string
	if err := db.QueryRow(`SELECT length(CAST(output_text AS BLOB)), output_sha256 FROM command_audits WHERE event_id = 'audit-0'`).Scan(&beforeLen, &beforeSHA); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	second := compactAuditEncodeStore(t, dbPath)
	step, ok := second.Steps.Find(application.CompactStepAuditEncode)
	if !ok {
		t.Fatalf("steps=%v, want audit_encode", second.Steps)
	}
	if step.Rows != 0 || step.Skipped != "" || step.Detail["rows_scanned"] != 0 {
		t.Fatalf("second audit_encode = %+v, want rows=0 skipped=\"\" rows_scanned=0", step)
	}

	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	var afterLen int64
	var afterSHA string
	if err := db.QueryRow(`SELECT length(CAST(output_text AS BLOB)), output_sha256 FROM command_audits WHERE event_id = 'audit-0'`).Scan(&afterLen, &afterSHA); err != nil {
		t.Fatal(err)
	}
	if afterLen != beforeLen || afterSHA != beforeSHA {
		t.Fatalf("stored output changed after second compact: len %d→%d sha %s→%s", beforeLen, afterLen, beforeSHA, afterSHA)
	}
}

func TestCompactAuditEncodeLeavesEncodedRowsAlone(t *testing.T) {
	ctx := context.Background()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	encoded := newGCEventFixture(t, "already-encoded", types.EventKindCommandExecuted, "", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	audit, err := model.NewCommandAudit(encoded.EventID(), "echo already", "in-already", compressibleAuditOutput(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.SaveWithAudit(ctx, encoded, audit); err != nil {
		t.Fatal(err)
	}
	seedNullCodecAudits(t, dbPath, 3, "echo", "in", compressibleAuditOutput())

	db := openRetentionDB(t, dbPath)
	var beforeLen int64
	var beforeCodec, beforeSHA string
	var beforeFmt, beforePlain, beforeEnc int64
	if err := db.QueryRow(`
		SELECT length(CAST(output_text AS BLOB)), output_codec, output_format_version,
		       output_plaintext_bytes, output_encoded_bytes, output_sha256
		  FROM command_audits WHERE event_id = 'already-encoded'`).Scan(
		&beforeLen, &beforeCodec, &beforeFmt, &beforePlain, &beforeEnc, &beforeSHA); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result := compactAuditEncodeStore(t, dbPath)
	step, ok := result.Steps.Find(application.CompactStepAuditEncode)
	if !ok {
		t.Fatalf("steps=%v, want audit_encode", result.Steps)
	}
	if step.Detail["rows_scanned"] != 3 {
		t.Fatalf("rows_scanned=%d, want 3 NULL-codec rows", step.Detail["rows_scanned"])
	}

	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	var afterLen int64
	var afterCodec, afterSHA string
	var afterFmt, afterPlain, afterEnc int64
	if err := db.QueryRow(`
		SELECT length(CAST(output_text AS BLOB)), output_codec, output_format_version,
		       output_plaintext_bytes, output_encoded_bytes, output_sha256
		  FROM command_audits WHERE event_id = 'already-encoded'`).Scan(
		&afterLen, &afterCodec, &afterFmt, &afterPlain, &afterEnc, &afterSHA); err != nil {
		t.Fatal(err)
	}
	if afterLen != beforeLen || afterCodec != beforeCodec || afterFmt != beforeFmt ||
		afterPlain != beforePlain || afterEnc != beforeEnc || afterSHA != beforeSHA {
		t.Fatalf("pre-encoded row changed: before (%d %s %d %d %d %s) after (%d %s %d %d %d %s)",
			beforeLen, beforeCodec, beforeFmt, beforePlain, beforeEnc, beforeSHA,
			afterLen, afterCodec, afterFmt, afterPlain, afterEnc, afterSHA)
	}
}

func TestCompactAuditEncodeFailsClosedOnPartialMetadata(t *testing.T) {
	dbPath := prepareAuditEncodeStore(t)
	seedNullCodecAudits(t, dbPath, 1, "echo", "in", "out")
	db := openRetentionDB(t, dbPath)
	if _, err := db.Exec(`UPDATE command_audits SET command_codec = 'zstd' WHERE event_id = 'audit-0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Log(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	svc := newAuditEncodeCompactionUsecase(t, dbPath)
	result, compactErr := svc.Compact(context.Background(), application.CompactInput{
		Source: dbPath, KeepDays: 36500, Now: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if compactErr == nil {
		t.Fatal("Compact() error = nil, want fail-closed on partial metadata")
	}
	if !strings.Contains(compactErr.Error(), "audit-0") || !strings.Contains(compactErr.Error(), "command") {
		t.Fatalf("error = %v, want event id and lane name", compactErr)
	}
	if result.Run.Phase == domain.CompactionCommitted {
		t.Fatalf("phase=%s, want not committed", result.Run.Phase)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("source file changed after a fail-closed compact")
	}
}

func TestCompactedAuditsStillDecodeOnListAndShow(t *testing.T) {
	ctx := context.Background()
	dbPath, events, _ := prepareDiscardGCFixture(t)
	const (
		command = "echo fixture-command"
		input   = "fixture-input"
		output  = "fixture-output"
	)
	ev := newGCEventFixture(t, "decode-me", types.EventKindCommandExecuted, "", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	db := openRetentionDB(t, dbPath)
	if err := events.Save(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?, 0, 0)`, "decode-me", command, input, output, len(input), len(output)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	compactAuditEncodeStore(t, dbPath)

	listed, err := events.ListRecent(ctx, 10, 0, types.EventKindCommandExecuted, "", "", "", "", false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := events.HydrateCommandAudits(ctx, listed, queryservice.FullCommandAuditPayload()); err != nil {
		t.Fatalf("HydrateCommandAudits: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed=%d, want 1", len(listed))
	}
	got, ok := listed[0].CommandAudit().Value()
	if !ok || got == nil {
		t.Fatal("missing hydrated command audit")
	}
	if got.Command() != command || got.Input() != input || got.Output() != output {
		t.Fatalf("hydrated command=%q input=%q output=%q", got.Command(), got.Input(), got.Output())
	}
}

func TestVerifyPairAcceptsReEncodedCommandAudits(t *testing.T) {
	ctx := context.Background()
	source := prepareAuditEncodeStore(t)
	seedNullCodecAudits(t, source, 2, "echo", "in", compressibleAuditOutput())
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	copyStoreFile(t, source, candidate)
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (infra.SQLiteCompactionBuilder{}).CompactInPlace(ctx, candidate, application.CompactFilter{}); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	if err := (infra.SQLiteCompactionBuilder{}).VerifyPair(ctx, source, candidate); err != nil {
		t.Fatalf("VerifyPair() error = %v, want accept re-encoded audits", err)
	}
}

func TestVerifyPairRejectsRewrittenCommandAuditPlaintext(t *testing.T) {
	ctx := context.Background()
	source := prepareAuditEncodeStore(t)
	const original = "original-output"
	seedNullCodecAudits(t, source, 1, "echo", "in", original)
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	copyStoreFile(t, source, candidate)
	tampered := original + "x"
	sum := sha256.Sum256([]byte(tampered))
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	if _, err := db.Exec(`
		UPDATE command_audits
		   SET output_text = ?,
		       output_codec = 'identity',
		       output_format_version = 1,
		       output_plaintext_bytes = ?,
		       output_encoded_bytes = ?,
		       output_sha256 = ?
		 WHERE event_id = 'audit-0'`,
		tampered, len(tampered), len(tampered), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err := (infra.SQLiteCompactionBuilder{}).VerifyPair(ctx, source, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of rewritten audit plaintext")
	}
	if !strings.Contains(err.Error(), "audit-0") {
		t.Fatalf("VerifyPair() error = %v, want event id", err)
	}
}

func TestVerifyPairRejectsDroppedCommandAudit(t *testing.T) {
	ctx := context.Background()
	source := prepareAuditEncodeStore(t)
	seedNullCodecAudits(t, source, 1, "echo", "in", "out")
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	copyStoreFile(t, source, candidate)
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM command_audits WHERE event_id = 'audit-0'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err := (infra.SQLiteCompactionBuilder{}).VerifyPair(ctx, source, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of dropped audit")
	}
	if !strings.Contains(err.Error(), "audit-0") {
		t.Fatalf("VerifyPair() error = %v, want event id", err)
	}
}

func TestVerifyPairRejectsChangedCommandAuditIdentity(t *testing.T) {
	ctx := context.Background()
	source := prepareAuditEncodeStore(t)
	seedNullCodecAudits(t, source, 1, "echo", "in", "out")
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	copyStoreFile(t, source, candidate)
	db := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, db)
	if _, err := db.Exec(`UPDATE command_audits SET exit_code = 99 WHERE event_id = 'audit-0'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	err := (infra.SQLiteCompactionBuilder{}).VerifyPair(ctx, source, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rejection of changed identity")
	}
	if !strings.Contains(err.Error(), "audit-0") {
		t.Fatalf("VerifyPair() error = %v, want event id", err)
	}
}

func TestVerifyPairAllowsAuditDroppedWithItsEvent(t *testing.T) {
	ctx := context.Background()
	source := prepareAuditEncodeStore(t)
	db := openRetentionDB(t, source)
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, client, agent, session_id, workspace, body, body_availability, created_at, source_hook)
		VALUES
		  ('evt-keep', 'prompt', 'hook', 'codex', 'session-1', 'repo', 'shared-body', 'available', '2026-08-01T00:00:00Z', ''),
		  ('evt-drop', 'prompt', 'hook', 'codex', 'session-1', 'repo', 'shared-body', 'available', '2026-08-01T00:00:01Z', '');
		INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated, input_original_bytes, output_original_bytes, exit_code, failed)
		VALUES
		  ('evt-keep', 'echo keep', '', '', 0, 0, 0, 0, 0, 0),
		  ('evt-drop', 'echo drop', '', '', 0, 0, 0, 0, 0, 0);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	copyStoreFile(t, source, candidate)
	cand := openRetentionDB(t, candidate)
	dropRetiredSearchFamily(t, cand)
	if _, err := cand.Exec(`DELETE FROM events WHERE id = 'evt-drop'`); err != nil {
		t.Fatal(err)
	}
	if err := cand.Close(); err != nil {
		t.Fatal(err)
	}
	sourceDB := openRetentionDB(t, source)
	defer func() { _ = sourceDB.Close() }()
	candidateDB := openRetentionDB(t, candidate)
	defer func() { _ = candidateDB.Close() }()
	if err := infra.VerifyCommandAuditsForTest(ctx, sourceDB, candidateDB, map[string]struct{}{"evt-keep": {}}); err != nil {
		t.Fatalf("verifyCommandAudits() error = %v, want accept audit dropped with its event", err)
	}
}

func TestSchemaDigestStillCoversCommandAudits(t *testing.T) {
	ctx := context.Background()
	source := prepareAuditEncodeStore(t)
	candidate := filepath.Join(filepath.Dir(source), "candidate.db")
	copyStoreFile(t, source, candidate)
	db := openRetentionDB(t, candidate)
	if _, err := db.Exec(`ALTER TABLE command_audits ADD COLUMN extra_identity_col TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	leftDB := openRetentionDB(t, source)
	defer func() { _ = leftDB.Close() }()
	rightDB := openRetentionDB(t, candidate)
	defer func() { _ = rightDB.Close() }()
	left, err := infra.SchemaDigestForTest(ctx, leftDB)
	if err != nil {
		t.Fatal(err)
	}
	right, err := infra.SchemaDigestForTest(ctx, rightDB)
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("schemaDigest ignored command_audits DDL change")
	}
}

func TestCompactAuditEncodeSkipsNonCounterCompatibilityState(t *testing.T) {
	dbPath := prepareAuditEncodeStore(t)
	seedNullCodecAudits(t, dbPath, 2, "echo", "in", compressibleAuditOutput())
	db := openRetentionDB(t, dbPath)
	if _, err := db.Exec(`UPDATE payload_codec_compatibility_state SET mode = 'legacy_index' WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result := compactAuditEncodeStore(t, dbPath)
	step, ok := result.Steps.Find(application.CompactStepAuditEncode)
	if !ok {
		t.Fatalf("steps=%v, want audit_encode", result.Steps)
	}
	if step.Skipped == "" || step.Rows != 0 {
		t.Fatalf("audit_encode = %+v, want skipped != \"\" and rows=0", step)
	}
	db = openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	var nullCodec int
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_audits WHERE command_codec IS NULL`).Scan(&nullCodec); err != nil {
		t.Fatal(err)
	}
	if nullCodec != 2 {
		t.Fatalf("NULL-codec rows after skip = %d, want 2", nullCodec)
	}
}

func prepareAuditEncodeStore(t *testing.T) string {
	t.Helper()
	dbPath, _, _ := prepareDiscardGCFixture(t)
	return dbPath
}

func newAuditEncodeCompactionUsecase(t *testing.T, dbPath string) application.StoreCompactionUsecase {
	t.Helper()
	return usecase.NewStoreCompactionUsecase(
		dbPath,
		&infra.CompactionFileJournal{Dir: filepath.Join(filepath.Dir(dbPath), ".traceary-compaction")},
		&infra.SQLiteCompactionBuilder{},
		infra.StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		infra.StoreLeaseCoordinator{},
	)
}

func compactAuditEncodeStore(t *testing.T, dbPath string) application.CompactResult {
	t.Helper()
	result, err := newAuditEncodeCompactionUsecase(t, dbPath).Compact(context.Background(), application.CompactInput{
		Source:   dbPath,
		KeepDays: 36500,
		Now:      time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if result.Run.Phase != domain.CompactionCommitted {
		t.Fatalf("phase=%s, want committed", result.Run.Phase)
	}
	return result
}

func seedNullCodecAudits(t *testing.T, dbPath string, n int, command, input, output string) {
	t.Helper()
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		id := "audit-" + strconv.Itoa(i)
		if _, err := tx.Exec(`
			INSERT INTO events(id, kind, client, agent, session_id, workspace, body, body_availability, created_at, source_hook)
			VALUES (?, 'command_executed', 'cli', 'codex', 'session-1', 'repo', '', 'available', ?, '')`,
			id, created); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.Exec(`
			INSERT INTO command_audits(
				event_id, command_text, input_text, output_text,
				input_truncated, output_truncated, input_original_bytes, output_original_bytes,
				exit_code, failed
			) VALUES (?, ?, ?, ?, 0, 0, ?, ?, 0, 0)`,
			id, command, input, output, len(input), len(output)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func compressibleAuditOutput() string {
	return strings.Repeat("redacted synthetic payload line\n", 2048)
}

func sha256Hex(plain []byte) string {
	sum := sha256.Sum256(plain)
	return hex.EncodeToString(sum[:])
}

func copyStoreFile(t *testing.T, src, dst string) {
	t.Helper()
	db := openRetentionDB(t, src)
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
