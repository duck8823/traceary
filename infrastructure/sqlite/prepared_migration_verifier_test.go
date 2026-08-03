package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
)

func TestPreparedMigrationVerifierAcceptsSchemaChangeAndLogicalCodecEquivalence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := dir + "/source.db"
	candidate := dir + "/candidate.db"
	if err := NewStoreManagementDatasource(NewDatabase(source, preparedMigrationsBefore(t, 35))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	writeDB, err := sql.Open("sqlite", directSQLiteRWDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	insertCanonicalVerifierFixture(ctx, t, writeDB)
	if err = writeDB.Close(); err != nil {
		t.Fatal(err)
	}
	sourceDB, err := openDirectReadOnly(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPreparedMigrationPlan(ctx, sourceDB, preparedMigrations(t))
	_ = sourceDB.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = copyFileExactForVerifier(source, candidate); err != nil {
		t.Fatal(err)
	}
	if err = NewStoreManagementDatasource(NewDatabase(candidate, preparedMigrations(t))).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	encoded, err := encodePayload([]byte("hello"), payloadCodecZstd)
	if err != nil {
		t.Fatal(err)
	}
	candidateDB, err := sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = candidateDB.ExecContext(ctx, `UPDATE events SET body=?,body_codec=?,body_format_version=?,body_plaintext_bytes=?,body_encoded_bytes=?,body_sha256=? WHERE id='e-1'`, encoded.Bytes, encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256); err != nil {
		t.Fatal(err)
	}
	// body_stored_bytes is canonical pre-codec provenance, not v36 codec
	// metadata. Preserve its source value while varying only codec storage.
	if _, err = candidateDB.ExecContext(ctx, `UPDATE events SET body_stored_bytes=5 WHERE id='e-1'`); err != nil {
		t.Fatal(err)
	}
	if err = candidateDB.Close(); err != nil {
		t.Fatal(err)
	}
	verifier := PreparedMigrationVerifier{Migrations: preparedMigrations(t)}
	evidence, err := verifier.VerifyPair(ctx, source, candidate, plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.MigrationSetDigest != plan.Digest || evidence.Canonical.EventCount != 1 || evidence.Canonical.AuditCount != 1 || len(evidence.SchemaDigest) != 64 {
		t.Fatalf("evidence = %+v", evidence)
	}
	candidateDB, err = sql.Open("sqlite", directSQLiteRWDSN(candidate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = candidateDB.ExecContext(ctx, `UPDATE events SET kind='changed' WHERE id='e-1'`); err != nil {
		t.Fatal(err)
	}
	if err = candidateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.VerifyPair(ctx, source, candidate, plan.Digest); err == nil {
		t.Fatal("verifier accepted canonical event drift")
	}
}

func insertCanonicalVerifierFixture(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO events(
		id,kind,agent,session_id,body,created_at,client,workspace,source_hook,
		body_original_bytes,body_stored_bytes,body_ingest_truncated,body_storage_truncated,body_metadata_version,
		body_availability,body_pruned_at,body_pruned_plan_id,created_at_norm
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"e-1", "command", "codex", "s-1", "hello", "2026-08-03T00:00:00Z", "cli", "/repo", nil,
		5, 5, 0, 0, 1, "available", nil, nil, "2026-08-03T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO command_audits(
		event_id,command_text,input_text,output_text,input_truncated,output_truncated,exit_code,failed,
		input_original_bytes,output_original_bytes,command_wrapper,command_name,failure_reason
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "e-1", "echo hi", "", "hi", 0, 0, 0, 0, 0, 2, "direct", "echo", "none"); err != nil {
		t.Fatal(err)
	}
}

func copyFileExactForVerifier(source, destination string) error {
	out, err := openFileNoFollow(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("close verifier candidate: %w", err)
	}
	return exactFileClone(source, destination)
}
