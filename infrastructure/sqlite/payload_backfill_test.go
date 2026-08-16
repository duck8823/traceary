package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

// compressibleBody returns plaintext that zstd shrinks; short unique strings
// often expand under zstd frame overhead and would stay identity by recipe.
func compressibleBody(tag string) string {
	return tag + " " + string(bytes.Repeat([]byte("redacted synthetic payload "), 128))
}

func TestPayloadBackfill(t *testing.T) {
	t.Parallel()

	t.Run("round-trip legacy plaintext", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "legacy-rt"
		body := compressibleBody("legacy")
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "note", Body: body, SourceHook: "stop"})

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if diff := cmp.Diff(string(apptypes.PayloadBackfillCompleted), result.State); diff != "" {
			t.Fatalf("state mismatch (-want +got):\n%s", diff)
		}

		got := readDecodedBody(t, db, id)
		if diff := cmp.Diff([]byte(body), got); diff != "" {
			t.Fatalf("decoded body mismatch (-want +got):\n%s", diff)
		}
		assertBodyCodec(t, db, id, payloadCodecZstd)
	})

	t.Run("round-trip identity write path", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "identity-rt"
		body := compressibleBody("identity")
		insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: body, SourceHook: "stop"})

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.State != string(apptypes.PayloadBackfillCompleted) {
			t.Fatalf("state = %q", result.State)
		}
		got := readDecodedBody(t, db, id)
		if diff := cmp.Diff([]byte(body), got); diff != "" {
			t.Fatalf("decoded body mismatch (-want +got):\n%s", diff)
		}
		assertBodyCodec(t, db, id, payloadCodecZstd)
	})

	t.Run("mixed corpus decodes every row", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		bodies := map[string]string{
			"mix-a": compressibleBody("mix-a"),
			"mix-b": compressibleBody("mix-b"),
			"mix-c": compressibleBody("mix-c"),
			"mix-d": compressibleBody("mix-d"),
		}
		insertPlaintextEvent(t, db, eventSeed{ID: "mix-a", Kind: "note", Body: bodies["mix-a"]})
		insertIdentityEvent(t, db, eventSeed{ID: "mix-b", Kind: "note", Body: bodies["mix-b"]})
		insertPlaintextEvent(t, db, eventSeed{ID: "mix-c", Kind: "note", Body: bodies["mix-c"]})
		// Pre-encode one row so the corpus is half encoded before the run.
		insertIdentityEvent(t, db, eventSeed{ID: "mix-d", Kind: "note", Body: bodies["mix-d"]})
		reencodeBodyToZstd(t, db, "mix-d", []byte(bodies["mix-d"]))

		if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		for id, want := range bodies {
			got := readDecodedBody(t, db, id)
			if diff := cmp.Diff([]byte(want), got); diff != "" {
				t.Fatalf("decoded body mismatch for %s (-want +got):\n%s", id, diff)
			}
		}
	})

	t.Run("partial metadata fails closed and leaves row untouched", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const (
			id   = "partial-meta"
			body = "partial metadata body stays plaintext"
		)
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "note", Body: body})
		// body_codec NULL but body_sha256 set → incomplete metadata.
		if _, err := db.Exec(`UPDATE events SET body_sha256 = ? WHERE id = ?`, "deadbeef", id); err != nil {
			t.Fatalf("seed partial metadata: %v", err)
		}
		before := readStoredBody(t, db, id)

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if !errors.Is(err, ErrPayloadBackfillPartialMetadata) {
			t.Fatalf("error = %v, want ErrPayloadBackfillPartialMetadata", err)
		}
		if result.State != string(apptypes.PayloadBackfillFailed) {
			t.Fatalf("state = %q, want failed", result.State)
		}
		if diff := cmp.Diff(id, result.FailureEventID); diff != "" {
			t.Fatalf("failure_event_id mismatch (-want +got):\n%s", diff)
		}
		after := readStoredBody(t, db, id)
		if !bytes.Equal(before, after) {
			t.Fatalf("partial-metadata row was rewritten")
		}
		var codec sql.NullString
		if err := db.QueryRow(`SELECT body_codec FROM events WHERE id = ?`, id).Scan(&codec); err != nil {
			t.Fatalf("read codec: %v", err)
		}
		if codec.Valid {
			t.Fatalf("body_codec became %q; row must stay untouched", codec.String)
		}
	})

	t.Run("incompressible body stays identity with metadata", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		// High-entropy bytes zstd cannot shrink.
		body := make([]byte, 256)
		for i := range body {
			body[i] = byte(i)
		}
		const id = "incompressible"
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "note", Body: string(body)})

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.State != string(apptypes.PayloadBackfillCompleted) {
			t.Fatalf("state = %q", result.State)
		}
		assertBodyCodec(t, db, id, payloadCodecIdentity)
		got := readDecodedBody(t, db, id)
		if diff := cmp.Diff(body, got); diff != "" {
			t.Fatalf("decoded body mismatch (-want +got):\n%s", diff)
		}
		// No longer rewritten on a second run.
		again, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("second Run: %v", err)
		}
		if again.RewrittenRows != 0 {
			t.Fatalf("second run rewritten_rows = %d, want 0", again.RewrittenRows)
		}
	})

	t.Run("idempotence does not re-encode zstd rows", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "idempotent"
		body := compressibleBody("idempotent")
		insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: body})
		if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
			t.Fatalf("first Run: %v", err)
		}
		before := readStoredBody(t, db, id)
		assertBodyCodec(t, db, id, payloadCodecZstd)

		again, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("second Run: %v", err)
		}
		if again.EncodedRows != 0 || again.RewrittenRows != 0 {
			t.Fatalf("second run encoded=%d rewritten=%d, want 0", again.EncodedRows, again.RewrittenRows)
		}
		after := readStoredBody(t, db, id)
		if !bytes.Equal(before, after) {
			t.Fatalf("already-encoded body bytes changed on second run")
		}
	})

	t.Run("resume after mid-run pause loses no row and double-encodes none", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		bodies := map[string]string{}
		for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
			bodies[id] = compressibleBody(id)
			insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: bodies[id]})
		}

		cfg := defaultBackfillConfig()
		cfg.BatchRows = 2
		cfg.StopAfterBatches = 1
		paused, err := ds.Run(ctx, cfg)
		if err != nil {
			t.Fatalf("Run (pause): %v", err)
		}
		if paused.State != string(apptypes.PayloadBackfillPaused) {
			t.Fatalf("state = %q, want paused", paused.State)
		}
		if paused.RewrittenRows == 0 {
			t.Fatalf("expected some rows rewritten before pause")
		}

		cfg.StopAfterBatches = 0
		done, err := ds.Resume(ctx, cfg)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if done.State != string(apptypes.PayloadBackfillCompleted) {
			t.Fatalf("state = %q, want completed", done.State)
		}
		for id, want := range bodies {
			got := readDecodedBody(t, db, id)
			if diff := cmp.Diff([]byte(want), got); diff != "" {
				t.Fatalf("decoded body mismatch for %s (-want +got):\n%s", id, diff)
			}
			assertBodyCodec(t, db, id, payloadCodecZstd)
		}
	})

	t.Run("recipe version mismatch refuses resume without skipping prefix", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		insertIdentityEvent(t, db, eventSeed{
			ID: "recipe-a", Kind: "note", Body: compressibleBody("recipe"),
		})
		// Seed a foreign recipe checkpoint that would skip the only row if honored.
		if _, err := db.Exec(`
			INSERT INTO payload_backfill_runs(
				run_id, recipe_version, high_water_rowid, cursor_rowid, pass_count, state,
				started_at, updated_at, scanned_rows, encoded_rows
			) VALUES ('foreign-run', 'other-recipe-v0', 999, 999, 1, 'paused',
			          '2026-08-09T00:00:00Z', '2026-08-09T00:00:00Z', 1, 0)`); err != nil {
			t.Fatalf("seed foreign recipe run: %v", err)
		}

		_, err := ds.Resume(ctx, defaultBackfillConfig())
		if !errors.Is(err, ErrPayloadBackfillRecipeMismatch) {
			t.Fatalf("error = %v, want ErrPayloadBackfillRecipeMismatch", err)
		}
		// Prefix must not have been treated as done: body still identity.
		assertBodyCodec(t, db, "recipe-a", payloadCodecIdentity)
	})

	t.Run("fixpoint encodes a row skipped for conflict on pass 1", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, path := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "conflict-skip"
		body := compressibleBody("conflict")
		insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: body})

		// Mutate the body between select and verify so the first pass skips it.
		// Rewrite full identity metadata for the new plaintext so a concurrent
		// reader that misses the conflict check still sees a coherent row; the
		// backfill must detect the stored-byte change via the source hash.
		mutated := body + "!"
		var conflicted bool
		ds.onBeforeCommitBatch = func(batch []backfillCandidate) {
			if conflicted || len(batch) == 0 {
				return
			}
			conflicted = true
			side, err := sql.Open("sqlite", directSQLiteRWDSN(path))
			if err != nil {
				t.Fatalf("open side conn: %v", err)
			}
			defer func() { _ = side.Close() }()
			encoded, err := encodePayload([]byte(mutated), payloadCodecIdentity)
			if err != nil {
				t.Fatalf("encode mutated identity: %v", err)
			}
			if _, err := side.Exec(`
				UPDATE events
				   SET body = ?,
				       body_codec = ?,
				       body_format_version = ?,
				       body_plaintext_bytes = ?,
				       body_encoded_bytes = ?,
				       body_sha256 = ?
				 WHERE id = ?`,
				string(encoded.Bytes), encoded.Codec, encoded.FormatVersion,
				encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256, id,
			); err != nil {
				t.Fatalf("conflict mutation: %v", err)
			}
		}

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.State != string(apptypes.PayloadBackfillCompleted) {
			t.Fatalf("state = %q", result.State)
		}
		if result.ConflictedRows < 1 {
			t.Fatalf("conflicted_rows = %d, want >= 1", result.ConflictedRows)
		}
		// After fixpoint the (mutated) body must be encoded, not stranded.
		assertBodyCodec(t, db, id, payloadCodecZstd)
		got := readDecodedBody(t, db, id)
		if diff := cmp.Diff([]byte(mutated), got); diff != "" {
			t.Fatalf("decoded body mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("high-water leaves rows inserted during the run as identity", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, path := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		for _, id := range []string{"hw-1", "hw-2", "hw-3"} {
			insertIdentityEvent(t, db, eventSeed{
				ID: id, Kind: "note", Body: compressibleBody(id),
			})
		}

		var inserted bool
		ds.onBeforeCommitBatch = func(_ []backfillCandidate) {
			if inserted {
				return
			}
			inserted = true
			side, err := sql.Open("sqlite", directSQLiteRWDSN(path))
			if err != nil {
				t.Fatalf("open side conn: %v", err)
			}
			defer func() { _ = side.Close() }()
			insertIdentityEvent(t, side, eventSeed{
				ID: "hw-new", Kind: "note", Body: compressibleBody("hw-new"),
			})
		}

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.State != string(apptypes.PayloadBackfillCompleted) {
			t.Fatalf("state = %q (run must terminate)", result.State)
		}
		assertBodyCodec(t, db, "hw-1", payloadCodecZstd)
		assertBodyCodec(t, db, "hw-2", payloadCodecZstd)
		assertBodyCodec(t, db, "hw-3", payloadCodecZstd)
		assertBodyCodec(t, db, "hw-new", payloadCodecIdentity)
	})

	t.Run("provenance survives backfill in events and projection", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "prov-backfill"
		body := compressibleBody("provenance")
		insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: body, SourceHook: "stop"})
		wantStored := int64(len(body))
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
		var wantOriginal sql.NullInt64
		if err := db.QueryRow(`SELECT body_original_bytes FROM events WHERE id = ?`, id).Scan(&wantOriginal); err != nil {
			t.Fatalf("read body_original_bytes: %v", err)
		}

		if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertBodyCodec(t, db, id, payloadCodecZstd)
		// Compressed size must differ so a silent overwrite is visible.
		var encodedBytes int64
		if err := db.QueryRow(`SELECT body_encoded_bytes FROM events WHERE id = ?`, id).Scan(&encodedBytes); err != nil {
			t.Fatalf("read body_encoded_bytes: %v", err)
		}
		if encodedBytes == wantStored {
			t.Fatalf("encoded size equals plaintext; provenance corruption would be invisible")
		}
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
		var gotOriginal, gotOriginalProj sql.NullInt64
		if err := db.QueryRow(`
			SELECT e.body_original_bytes, p.body_original_bytes
			  FROM events e JOIN event_metadata_projection p ON p.id = e.id
			 WHERE e.id = ?`, id).Scan(&gotOriginal, &gotOriginalProj); err != nil {
			t.Fatalf("read body_original_bytes after: %v", err)
		}
		if diff := cmp.Diff(wantOriginal, gotOriginal); diff != "" {
			t.Fatalf("events.body_original_bytes mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(wantOriginal, gotOriginalProj); diff != "" {
			t.Fatalf("projection.body_original_bytes mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("compatibility counter matches encoded count", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		for _, id := range []string{"cc-1", "cc-2", "cc-3"} {
			insertIdentityEvent(t, db, eventSeed{
				ID: id, Kind: "note", Body: compressibleBody(id),
			})
		}
		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var nonIdentity int64
		if err := db.QueryRow(`SELECT event_body_nonidentity FROM payload_codec_compatibility_state WHERE singleton = 1`).Scan(&nonIdentity); err != nil {
			t.Fatalf("read compatibility counter: %v", err)
		}
		if diff := cmp.Diff(result.EncodedRows, nonIdentity); diff != "" {
			t.Fatalf("event_body_nonidentity mismatch (-encoded +counter):\n%s", diff)
		}
		if nonIdentity != 3 {
			t.Fatalf("event_body_nonidentity = %d, want 3", nonIdentity)
		}
	})

	t.Run("canonical audit digest is unchanged", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		insertIdentityEvent(t, db, eventSeed{
			ID: "canon-1", Kind: "note", Body: compressibleBody("canon-1"),
		})
		insertPlaintextEvent(t, db, eventSeed{
			ID: "canon-2", Kind: "note", Body: compressibleBody("canon-2"),
		})
		insertPlaintextEvent(t, db, eventSeed{
			ID: "canon-cmd", Kind: "command_executed", Body: "",
		})
		insertPlaintextAudit(t, db, auditSeed{
			EventID:             "canon-cmd",
			Command:             compressibleBody("canon-cmd"),
			Input:               compressibleBody("canon-in"),
			Output:              compressibleBody("canon-out"),
			InputOriginalBytes:  111,
			OutputOriginalBytes: 222,
		})

		before, err := CanonicalEventAuditDigest(ctx, db)
		if err != nil {
			t.Fatalf("digest before: %v", err)
		}
		if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		after, err := CanonicalEventAuditDigest(ctx, db)
		if err != nil {
			t.Fatalf("digest after: %v", err)
		}
		if diff := cmp.Diff(before.Digest, after.Digest); diff != "" {
			t.Fatalf("canonical digest mismatch (-before +after):\n%s", diff)
		}
		if before.EventCount != after.EventCount {
			t.Fatalf("event count %d → %d", before.EventCount, after.EventCount)
		}
		if before.AuditCount != after.AuditCount {
			t.Fatalf("audit count %d → %d", before.AuditCount, after.AuditCount)
		}
	})
}

// Command-audit lanes share the events.body engine: same predicate, fixpoint,
// high-water, and recipe version. These cases pin the three extra fields and
// the provenance columns the issue forbids rewriting.
func TestPayloadBackfillCommandAudits(t *testing.T) {
	t.Parallel()

	t.Run("round-trips all three audit text fields", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "audit-rt"
		want := auditSeed{
			EventID:             id,
			Command:             compressibleBody("cmd"),
			Input:               compressibleBody("in"),
			Output:              compressibleBody("out"),
			InputOriginalBytes:  9001,
			OutputOriginalBytes: 9002,
		}
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
		insertPlaintextAudit(t, db, want)

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if diff := cmp.Diff(string(apptypes.PayloadBackfillCompleted), result.State); diff != "" {
			t.Fatalf("state mismatch (-want +got):\n%s", diff)
		}
		assertAuditCodec(t, db, id, "command", payloadCodecZstd)
		assertAuditCodec(t, db, id, "input", payloadCodecZstd)
		assertAuditCodec(t, db, id, "output", payloadCodecZstd)
		got := readDecodedAudit(t, db, id)
		if diff := cmp.Diff(want.Command, got.Command); diff != "" {
			t.Fatalf("command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want.Input, got.Input); diff != "" {
			t.Fatalf("input mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want.Output, got.Output); diff != "" {
			t.Fatalf("output mismatch (-want +got):\n%s", diff)
		}
		assertAuditOriginalBytes(t, db, id, want.InputOriginalBytes, want.OutputOriginalBytes)
	})

	t.Run("round-trips identity write path for audits", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "audit-identity-rt"
		want := auditSeed{
			EventID:             id,
			Command:             compressibleBody("id-cmd"),
			Input:               compressibleBody("id-in"),
			Output:              compressibleBody("id-out"),
			InputOriginalBytes:  42,
			OutputOriginalBytes: 43,
		}
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
		insertIdentityAudit(t, db, want)

		if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertAuditCodec(t, db, id, "command", payloadCodecZstd)
		assertAuditCodec(t, db, id, "input", payloadCodecZstd)
		assertAuditCodec(t, db, id, "output", payloadCodecZstd)
		got := readDecodedAudit(t, db, id)
		if diff := cmp.Diff(want.Command, got.Command); diff != "" {
			t.Fatalf("command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want.Input, got.Input); diff != "" {
			t.Fatalf("input mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(want.Output, got.Output); diff != "" {
			t.Fatalf("output mismatch (-want +got):\n%s", diff)
		}
		assertAuditOriginalBytes(t, db, id, want.InputOriginalBytes, want.OutputOriginalBytes)
	})

	t.Run("partial audit metadata fails closed and leaves fields untouched", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "audit-partial"
		seed := auditSeed{
			EventID: id,
			Command: "partial command stays plaintext",
			Input:   "partial input stays plaintext",
			Output:  "partial output stays plaintext",
		}
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
		insertPlaintextAudit(t, db, seed)
		// command_codec NULL but command_sha256 set → incomplete metadata.
		if _, err := db.Exec(`UPDATE command_audits SET command_sha256 = ? WHERE event_id = ?`, "deadbeef", id); err != nil {
			t.Fatalf("seed partial metadata: %v", err)
		}
		before := readStoredAudit(t, db, id)

		result, err := ds.Run(ctx, defaultBackfillConfig())
		if !errors.Is(err, ErrPayloadBackfillPartialMetadata) {
			t.Fatalf("error = %v, want ErrPayloadBackfillPartialMetadata", err)
		}
		if result.State != string(apptypes.PayloadBackfillFailed) {
			t.Fatalf("state = %q, want failed", result.State)
		}
		if diff := cmp.Diff(id, result.FailureEventID); diff != "" {
			t.Fatalf("failure_event_id mismatch (-want +got):\n%s", diff)
		}
		after := readStoredAudit(t, db, id)
		if diff := cmp.Diff(before, after); diff != "" {
			t.Fatalf("partial-metadata row was rewritten (-before +after):\n%s", diff)
		}
	})

	t.Run("compatibility counters match encoded audit fields", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		for _, id := range []string{"cc-a1", "cc-a2"} {
			insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
			insertIdentityAudit(t, db, auditSeed{
				EventID: id,
				Command: compressibleBody(id + "-cmd"),
				Input:   compressibleBody(id + "-in"),
				Output:  compressibleBody(id + "-out"),
			})
		}
		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var commandN, inputN, outputN int64
		if err := db.QueryRow(`
			SELECT audit_command_nonidentity, audit_input_nonidentity, audit_output_nonidentity
			  FROM payload_codec_compatibility_state WHERE singleton = 1`).Scan(&commandN, &inputN, &outputN); err != nil {
			t.Fatalf("read audit counters: %v", err)
		}
		if commandN != 2 || inputN != 2 || outputN != 2 {
			t.Fatalf("audit nonidentity counters = command %d input %d output %d, want 2 each", commandN, inputN, outputN)
		}
		// Six audit fields encoded; event bodies empty/identity may or may not
		// compress. EncodedRows must at least cover the six audit fields.
		if result.EncodedRows < 6 {
			t.Fatalf("encoded_rows = %d, want >= 6", result.EncodedRows)
		}
	})

	t.Run("audit-only corpus completes under the shared high-water", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		// No event body work: empty bodies are tiny and stay identity. The
		// run must still walk command_audits and terminate.
		const id = "audit-only"
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
		insertPlaintextAudit(t, db, auditSeed{
			EventID: id,
			Command: compressibleBody("only-cmd"),
			Input:   compressibleBody("only-in"),
			Output:  compressibleBody("only-out"),
		})
		result, err := ds.Run(ctx, defaultBackfillConfig())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.State != string(apptypes.PayloadBackfillCompleted) {
			t.Fatalf("state = %q, want completed", result.State)
		}
		assertAuditCodec(t, db, id, "command", payloadCodecZstd)
		assertAuditCodec(t, db, id, "input", payloadCodecZstd)
		assertAuditCodec(t, db, id, "output", payloadCodecZstd)
	})

	t.Run("input_original_bytes and output_original_bytes are unchanged", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		db, ds, _ := openPayloadBackfillFixture(t)
		defer closePayloadBackfillFixture(t, db)

		const id = "audit-orig"
		const wantIn, wantOut int64 = 12345, 67890
		insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
		insertIdentityAudit(t, db, auditSeed{
			EventID:             id,
			Command:             compressibleBody("orig-cmd"),
			Input:               compressibleBody("orig-in"),
			Output:              compressibleBody("orig-out"),
			InputOriginalBytes:  wantIn,
			OutputOriginalBytes: wantOut,
		})
		if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		// Codecs must have changed so a silent no-op would not hide a write.
		assertAuditCodec(t, db, id, "output", payloadCodecZstd)
		assertAuditOriginalBytes(t, db, id, wantIn, wantOut)
	})
}

type auditSeed struct {
	EventID             string
	Command             string
	Input               string
	Output              string
	InputOriginalBytes  int64
	OutputOriginalBytes int64
}

type decodedAudit struct {
	Command string
	Input   string
	Output  string
}

type storedAudit struct {
	Command []byte
	Input   []byte
	Output  []byte
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

func insertIdentityAudit(t *testing.T, db *sql.DB, seed auditSeed) {
	t.Helper()
	command, err := encodePayload([]byte(seed.Command), payloadCodecIdentity)
	if err != nil {
		t.Fatalf("encode command identity: %v", err)
	}
	input, err := encodePayload([]byte(seed.Input), payloadCodecIdentity)
	if err != nil {
		t.Fatalf("encode input identity: %v", err)
	}
	output, err := encodePayload([]byte(seed.Output), payloadCodecIdentity)
	if err != nil {
		t.Fatalf("encode output identity: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO command_audits(
			event_id, command_text, input_text, output_text,
			input_truncated, output_truncated, input_original_bytes, output_original_bytes,
			exit_code, failed,
			command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256,
			input_codec, input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256,
			output_codec, output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256
		) VALUES (?, ?, ?, ?, 0, 0, ?, ?, 0, 0,
		          ?, ?, ?, ?, ?,
		          ?, ?, ?, ?, ?,
		          ?, ?, ?, ?, ?)`,
		seed.EventID, string(command.Bytes), string(input.Bytes), string(output.Bytes),
		seed.InputOriginalBytes, seed.OutputOriginalBytes,
		command.Codec, command.FormatVersion, command.PlaintextBytes, command.StoredBytes, command.SHA256,
		input.Codec, input.FormatVersion, input.PlaintextBytes, input.StoredBytes, input.SHA256,
		output.Codec, output.FormatVersion, output.PlaintextBytes, output.StoredBytes, output.SHA256,
	); err != nil {
		t.Fatalf("insert identity audit %s: %v", seed.EventID, err)
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

func readDecodedAudit(t *testing.T, db *sql.DB, eventID string) decodedAudit {
	t.Helper()
	ctx := context.Background()
	command, err := hydrateAuditPayload(ctx, db, eventID, "command")
	if err != nil {
		t.Fatalf("hydrate command: %v", err)
	}
	input, err := hydrateAuditPayload(ctx, db, eventID, "input")
	if err != nil {
		t.Fatalf("hydrate input: %v", err)
	}
	output, err := hydrateAuditPayload(ctx, db, eventID, "output")
	if err != nil {
		t.Fatalf("hydrate output: %v", err)
	}
	return decodedAudit{Command: command.String, Input: input.String, Output: output.String}
}

func readStoredAudit(t *testing.T, db *sql.DB, eventID string) storedAudit {
	t.Helper()
	var got storedAudit
	if err := db.QueryRow(`
		SELECT command_text, input_text, output_text FROM command_audits WHERE event_id = ?`, eventID,
	).Scan(&got.Command, &got.Input, &got.Output); err != nil {
		t.Fatalf("read stored audit %s: %v", eventID, err)
	}
	return got
}

func assertAuditCodec(t *testing.T, db *sql.DB, eventID, field, want string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRow(`SELECT `+field+`_codec FROM command_audits WHERE event_id = ?`, eventID).Scan(&got); err != nil {
		t.Fatalf("read %s_codec %s: %v", field, eventID, err)
	}
	if !got.Valid {
		t.Fatalf("%s_codec for %s is NULL, want %q", field, eventID, want)
	}
	if diff := cmp.Diff(want, got.String); diff != "" {
		t.Fatalf("%s_codec mismatch for %s (-want +got):\n%s", field, eventID, diff)
	}
}

func assertAuditOriginalBytes(t *testing.T, db *sql.DB, eventID string, wantIn, wantOut int64) {
	t.Helper()
	var gotIn, gotOut int64
	if err := db.QueryRow(`
		SELECT input_original_bytes, output_original_bytes FROM command_audits WHERE event_id = ?`, eventID,
	).Scan(&gotIn, &gotOut); err != nil {
		t.Fatalf("read original bytes %s: %v", eventID, err)
	}
	if gotIn != wantIn || gotOut != wantOut {
		t.Fatalf("original bytes = input %d output %d, want input %d output %d", gotIn, gotOut, wantIn, wantOut)
	}
}

func defaultBackfillConfig() apptypes.PayloadBackfillConfig {
	return apptypes.PayloadBackfillConfig{BatchRows: apptypes.DefaultPayloadBackfillBatchRows}
}

func openPayloadBackfillFixture(t *testing.T) (*sql.DB, *PayloadBackfillDatasource, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "payload-backfill.db")
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
	return db, NewPayloadBackfillDatasource(database), path
}

func closePayloadBackfillFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

func readDecodedBody(t *testing.T, db *sql.DB, id string) []byte {
	t.Helper()
	var row payloadRow
	if err := db.QueryRow(`
		SELECT body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		  FROM events WHERE id = ?`, id).Scan(row.scanDestinations()...); err != nil {
		t.Fatalf("read payload row %s: %v", id, err)
	}
	decoded, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		t.Fatalf("decode payload row %s: %v", id, err)
	}
	return decoded
}

func readStoredBody(t *testing.T, db *sql.DB, id string) []byte {
	t.Helper()
	var body []byte
	if err := db.QueryRow(`SELECT body FROM events WHERE id = ?`, id).Scan(&body); err != nil {
		t.Fatalf("read stored body %s: %v", id, err)
	}
	return body
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
	if diff := cmp.Diff(want, got.String); diff != "" {
		t.Fatalf("body_codec mismatch for %s (-want +got):\n%s", id, diff)
	}
}

// A store that applied the original migration 36 is left in legacy_index mode
// by migration 043. Migration 036's payload_codec_events_update trigger updates
// the counter row WHERE mode='counter' AND state='valid' and RAISE(ABORT)s when
// that matched nothing, so on such a store every identity -> zstd transition
// aborts its batch. The run must refuse up front and name the reason instead of
// leaving an open run that can never advance.
func TestPayloadBackfillRefusesNonCounterCompatibilityMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	insertIdentityEvent(t, db, eventSeed{ID: "legacy-mode", Kind: "note", Body: compressibleBody("legacy-mode")})
	if _, err := db.Exec(`UPDATE payload_codec_compatibility_state SET mode='legacy_index' WHERE singleton=1`); err != nil {
		t.Fatalf("switch compatibility mode: %v", err)
	}

	for name, call := range map[string]func() (apptypes.PayloadBackfillResult, error){
		"preview": func() (apptypes.PayloadBackfillResult, error) {
			return ds.Preview(ctx, defaultBackfillConfig())
		},
		"run": func() (apptypes.PayloadBackfillResult, error) {
			return ds.Run(ctx, defaultBackfillConfig())
		},
	} {
		name, call := name, call
		t.Run(name+" refuses legacy_index mode", func(t *testing.T) {
			if _, err := call(); !errors.Is(err, ErrPayloadBackfillCompatibilityMode) {
				t.Fatalf("%s error = %v, want ErrPayloadBackfillCompatibilityMode", name, err)
			}
		})
	}

	t.Run("no run row is left behind", func(t *testing.T) {
		var runs int
		if err := db.QueryRow(`SELECT COUNT(*) FROM payload_backfill_runs`).Scan(&runs); err != nil {
			t.Fatalf("count runs: %v", err)
		}
		if runs != 0 {
			t.Fatalf("runs = %d, want 0", runs)
		}
	})

	t.Run("the row is untouched", func(t *testing.T) {
		assertBodyCodec(t, db, "legacy-mode", payloadCodecIdentity)
	})
}

// A row whose five codec columns are neither all-NULL nor all-present is
// unreadable, and the recipe fails the run closed on it. Selecting only
// identity rows would let a corrupt non-identity row sit out an entire walk
// uninspected and report a clean completion over a store no reader can decode.
func TestPayloadBackfillFailsClosedOnCorruptNonIdentityRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	insertIdentityEvent(t, db, eventSeed{ID: "healthy", Kind: "note", Body: compressibleBody("healthy")})
	insertIdentityEvent(t, db, eventSeed{ID: "corrupt-zstd", Kind: "note", Body: compressibleBody("corrupt")})
	// Claim zstd while dropping the digest: a codec value the eligibility arm
	// skips, on a row the coherence check must still see.
	if _, err := db.Exec(`UPDATE events SET body_codec='zstd', body_sha256=NULL WHERE id='corrupt-zstd'`); err != nil {
		t.Fatalf("corrupt the row: %v", err)
	}

	result, err := ds.Run(ctx, defaultBackfillConfig())
	if !errors.Is(err, ErrPayloadBackfillPartialMetadata) {
		t.Fatalf("Run error = %v, want ErrPayloadBackfillPartialMetadata", err)
	}
	if diff := cmp.Diff("corrupt-zstd", result.FailureEventID); diff != "" {
		t.Fatalf("failure event id mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillFailed), result.State); diff != "" {
		t.Fatalf("run state mismatch (-want +got):\n%s", diff)
	}
	// The batch rolls back whole, so the healthy row is left for a later run.
	assertBodyCodec(t, db, "healthy", payloadCodecIdentity)
}

// Migration 054 admits one active run row, not one worker. A second resume of
// the same run must not keep advancing it after the first worker terminated it,
// because a resurrected 'running' row misreports status and blocks a new run.
func TestPayloadBackfillCheckpointRefusesToReviveATerminatedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	insertIdentityEvent(t, db, eventSeed{ID: "revive-1", Kind: "note", Body: compressibleBody("revive-1")})

	// Stand in for the other worker: terminate the run after this batch was
	// selected but before its checkpoint lands.
	ds.onBeforeCommitBatch = func([]backfillCandidate) {
		if _, err := db.Exec(`UPDATE payload_backfill_runs SET state='failed' WHERE state='running'`); err != nil {
			t.Errorf("terminate run from the other worker: %v", err)
		}
	}

	if _, err := ds.Run(ctx, defaultBackfillConfig()); !errors.Is(err, ErrPayloadBackfillRunPreempted) {
		t.Fatalf("Run error = %v, want ErrPayloadBackfillRunPreempted", err)
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&state); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillFailed), state); diff != "" {
		t.Fatalf("run state mismatch (-want +got):\n%s", diff)
	}
	// The batch transaction carries the checkpoint, so losing the run rolls the
	// rewrite back too.
	assertBodyCodec(t, db, "revive-1", payloadCodecIdentity)
}

// Cancelling the run is how an operator stops it, so the pause has to be
// persisted on a context that is not the cancelled one. Otherwise the run is
// left at 'running': resumable, but reported active and blocking a new run.
func TestPayloadBackfillPersistsPauseOnCancellation(t *testing.T) {
	t.Parallel()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	for i := range 3 {
		insertIdentityEvent(t, db, eventSeed{
			ID: "cancel-" + strconv.Itoa(i), Kind: "note", Body: compressibleBody("cancel"),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ds.onAfterCommitBatch = func(int64) { cancel() }

	result, err := ds.Run(ctx, apptypes.PayloadBackfillConfig{BatchRows: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), result.State); diff != "" {
		t.Fatalf("result state mismatch (-want +got):\n%s", diff)
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&state); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), state); diff != "" {
		t.Fatalf("persisted state mismatch (-want +got):\n%s", diff)
	}
}

func TestPayloadBackfillCheckpointWindowStartsAtEachTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		stopAfterBatch int64
		wantState      string
	}{
		{
			name:      "completion after a slow committed batch",
			wantState: string(apptypes.PayloadBackfillCompleted),
		},
		{
			name:           "pause after a slow committed batch",
			stopAfterBatch: 1,
			wantState:      string(apptypes.PayloadBackfillPaused),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, ds, _ := openPayloadBackfillFixture(t)
			defer closePayloadBackfillFixture(t, db)
			insertIdentityEvent(t, db, eventSeed{
				ID: "checkpoint-window", Kind: "note", Body: compressibleBody("checkpoint-window"),
			})

			// The window has to outlast two SQLite writes on a loaded runner
			// while the sleep still overruns it several times over. Shrinking
			// both further would test the scheduler rather than the anchoring.
			ds.checkpointTimeout = 150 * time.Millisecond
			var once sync.Once
			ds.onAfterCommitBatch = func(int64) {
				once.Do(func() { time.Sleep(750 * time.Millisecond) })
			}

			result, err := ds.Run(context.Background(), apptypes.PayloadBackfillConfig{
				BatchRows:        1,
				StopAfterBatches: tt.stopAfterBatch,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if diff := cmp.Diff(tt.wantState, result.State); diff != "" {
				t.Fatalf("result state mismatch (-want +got):\n%s", diff)
			}

			var persistedState string
			if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&persistedState); err != nil {
				t.Fatalf("read persisted state: %v", err)
			}
			if diff := cmp.Diff(tt.wantState, persistedState); diff != "" {
				t.Fatalf("persisted state mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Identity bodies are bound as TEXT to preserve the column affinity readers
// and triggers expect. SQLite TEXT is a byte string, not validated UTF-8, but
// the rewrite has to prove it: a legacy body is arbitrary bytes and losing any
// of them would be silent corruption of retained history.
func TestPayloadBackfillPreservesNonUTF8IdentityBodies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	// Invalid UTF-8, an embedded NUL, and a lone surrogate — short enough that
	// zstd expands it, so the recipe keeps identity and rewrites in place.
	raw := string([]byte{0xff, 0xfe, 0x00, 'a', 0xed, 0xa0, 0x80, 'b'})
	insertPlaintextEvent(t, db, eventSeed{ID: "raw-bytes", Kind: "note", Body: raw})

	if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertBodyCodec(t, db, "raw-bytes", payloadCodecIdentity)
	if diff := cmp.Diff([]byte(raw), readDecodedBody(t, db, "raw-bytes")); diff != "" {
		t.Fatalf("decoded body mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]byte(raw), readStoredBody(t, db, "raw-bytes")); diff != "" {
		t.Fatalf("stored body mismatch (-want +got):\n%s", diff)
	}
	// The affinity itself is the point: a BLOB here breaks migration 053.
	var storedType string
	if err := db.QueryRow(`SELECT typeof(body) FROM events WHERE id = 'raw-bytes'`).Scan(&storedType); err != nil {
		t.Fatalf("read body type: %v", err)
	}
	if diff := cmp.Diff("text", storedType); diff != "" {
		t.Fatalf("stored body type mismatch (-want +got):\n%s", diff)
	}
}

// Cancellation can surface at the top of the loop or out of the select/commit
// call that was in flight. All three have to land the same paused checkpoint,
// or the operator is left with a run that status reports as active.
func TestPayloadBackfillPersistsPauseWhenCancelledMidBatch(t *testing.T) {
	t.Parallel()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	for i := range 3 {
		insertIdentityEvent(t, db, eventSeed{
			ID: "mid-" + strconv.Itoa(i), Kind: "note", Body: compressibleBody("mid"),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel while the batch is selected but before its transaction runs, so
	// the cancellation surfaces out of commitBackfillBatch, not out of the
	// loop's own ctx.Err() check.
	ds.onBeforeCommitBatch = func([]backfillCandidate) { cancel() }

	result, err := ds.Run(ctx, apptypes.PayloadBackfillConfig{BatchRows: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), result.State); diff != "" {
		t.Fatalf("result state mismatch (-want +got):\n%s", diff)
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&state); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), state); diff != "" {
		t.Fatalf("persisted state mismatch (-want +got):\n%s", diff)
	}
}

// A resume takes the run over by re-stamping its worker token. The active-run
// index admits one run row, not one worker, so without the token two live
// workers would both satisfy state = 'running' and interleave cursor writes and
// counters into the same run.
func TestPayloadBackfillResumeFencesTheWorkerItTookOver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, path := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	for i := range 4 {
		insertIdentityEvent(t, db, eventSeed{
			ID: "fence-" + strconv.Itoa(i), Kind: "note", Body: compressibleBody("fence"),
		})
	}

	// First worker pauses mid-run, leaving a resumable checkpoint.
	if _, err := ds.Run(ctx, apptypes.PayloadBackfillConfig{BatchRows: 1, StopAfterBatches: 1}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Second worker takes the run over between the first worker's batches.
	first := NewPayloadBackfillDatasource(NewDatabase(path, preparedMigrations(t)))
	var tookOver bool
	first.onBeforeCommitBatch = func([]backfillCandidate) {
		if tookOver {
			return
		}
		tookOver = true
		// Claim the run the way a second resume does, and stop there: the run
		// stays 'running', so only the token can tell the two workers apart.
		var runID string
		if err := db.QueryRow(`SELECT run_id FROM payload_backfill_runs WHERE state = 'running'`).Scan(&runID); err != nil {
			t.Errorf("read the running run: %v", err)
			return
		}
		if err := markBackfillRunningFromOrigin(ctx, db, runID, "takeover-token"); err != nil {
			t.Errorf("takeover claim: %v", err)
		}
	}

	if _, err := first.Resume(ctx, apptypes.PayloadBackfillConfig{BatchRows: 1}); !errors.Is(err, ErrPayloadBackfillRunPreempted) {
		t.Fatalf("fenced worker error = %v, want ErrPayloadBackfillRunPreempted", err)
	}
}

// The partial-metadata failure is the only signal that names the corrupt row.
// If the run was taken over before the record lands, the caller has to hear
// that the failure was never recorded rather than a failure it can act on.
func TestPayloadBackfillReportsPreemptionWhenAFailureCannotBeRecorded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	insertIdentityEvent(t, db, eventSeed{ID: "corrupt", Kind: "note", Body: compressibleBody("corrupt")})
	if _, err := db.Exec(`UPDATE events SET body_codec='zstd', body_sha256=NULL WHERE id='corrupt'`); err != nil {
		t.Fatalf("corrupt the row: %v", err)
	}
	// Another worker terminates the run between the batch rollback and the
	// failure record.
	ds.onBeforeCommitBatch = func([]backfillCandidate) {
		if _, err := db.Exec(`UPDATE payload_backfill_runs SET state='completed' WHERE state='running'`); err != nil {
			t.Errorf("terminate run from the other worker: %v", err)
		}
	}

	if _, err := ds.Run(ctx, defaultBackfillConfig()); !errors.Is(err, ErrPayloadBackfillRunPreempted) {
		t.Fatalf("Run error = %v, want ErrPayloadBackfillRunPreempted", err)
	}
	var failureEventID sql.NullString
	if err := db.QueryRow(`SELECT failure_event_id FROM payload_backfill_runs`).Scan(&failureEventID); err != nil {
		t.Fatalf("read failure event id: %v", err)
	}
	if failureEventID.Valid {
		t.Fatalf("failure_event_id = %q; a lost run must not be overwritten", failureEventID.String)
	}
}

// Cancellation between a committed batch and the --stop-after-batches check
// still has to land the paused checkpoint. The batch is durable, so a run left
// at 'running' here reports itself active and refuses the next Run.
func TestPayloadBackfillPersistsPauseWhenCancelledAfterTheLastBatch(t *testing.T) {
	t.Parallel()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	for i := range 3 {
		insertIdentityEvent(t, db, eventSeed{
			ID: "after-" + strconv.Itoa(i), Kind: "note", Body: compressibleBody("after"),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ds.onAfterCommitBatch = func(int64) { cancel() }

	// The hook cancels synchronously, so the batch is already durable and the
	// stop is the one the caller asked for: this returns the paused run, not an
	// error. Accepting context.Canceled here would let the checkpoint read fall
	// back to the cancelled context unnoticed.
	result, err := ds.Run(ctx, apptypes.PayloadBackfillConfig{BatchRows: 1, StopAfterBatches: 1})
	if err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), result.State); diff != "" {
		t.Fatalf("result state mismatch (-want +got):\n%s", diff)
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&state); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), state); diff != "" {
		t.Fatalf("persisted state mismatch (-want +got):\n%s", diff)
	}
}

// A real failure that races a cancellation is still the failure the operator
// needs to see. The checkpoint lands either way; only the reported error
// depends on what actually stopped the batch.
func TestPayloadBackfillReportsTheRealErrorThatRacedACancellation(t *testing.T) {
	t.Parallel()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	insertIdentityEvent(t, db, eventSeed{ID: "race-0", Kind: "note", Body: compressibleBody("race")})

	ctx, cancel := context.WithCancel(context.Background())
	run, err := prepareBackfillRun(ctx, db, false)
	if err != nil {
		t.Fatalf("prepareBackfillRun: %v", err)
	}
	cancel()

	diskFull := errors.New("disk I/O error")
	result, got := ds.abortRun(ctx, db, run, 1, diskFull)
	if !errors.Is(got, diskFull) {
		t.Fatalf("abortRun error = %v, want the disk I/O error", got)
	}
	if strings.Contains(got.Error(), "cancelled") {
		t.Fatalf("abortRun reported a real failure as a cancellation: %v", got)
	}
	// A cancellation reports the paused run it checkpointed; a failure reports
	// no run, because the run is not what went wrong.
	if diff := cmp.Diff(apptypes.PayloadBackfillResult{}, result); diff != "" {
		t.Fatalf("failure result mismatch (-want +got):\n%s", diff)
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&state); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), state); diff != "" {
		t.Fatalf("persisted state mismatch (-want +got):\n%s", diff)
	}
}

// Migration 054 is unreleased and was edited in place, so a store built from an
// earlier revision of this branch keeps the old table and the migrator will not
// touch it again. Name that store instead of failing deep inside a query.
func TestPayloadBackfillRefusesAPreReleaseRunTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Every public entry point that reads the run table has to refuse it, not
	// just the one an operator happens to try first.
	entries := map[string]func(*PayloadBackfillDatasource) error{
		"preview": func(ds *PayloadBackfillDatasource) error {
			_, err := ds.Preview(ctx, defaultBackfillConfig())
			return err
		},
		"status": func(ds *PayloadBackfillDatasource) error { _, err := ds.Status(ctx); return err },
		"run": func(ds *PayloadBackfillDatasource) error {
			_, err := ds.Run(ctx, defaultBackfillConfig())
			return err
		},
		"resume": func(ds *PayloadBackfillDatasource) error {
			_, err := ds.Resume(ctx, defaultBackfillConfig())
			return err
		},
	}

	for _, missing := range []string{"worker_token", "audit_high_water_rowid"} {
		missing := missing
		t.Run("missing "+missing, func(t *testing.T) {
			t.Parallel()
			db, ds, _ := openPayloadBackfillFixture(t)
			defer closePayloadBackfillFixture(t, db)
			if _, err := db.Exec(`ALTER TABLE payload_backfill_runs DROP COLUMN ` + missing); err != nil {
				t.Fatalf("seed pre-release run table without %s: %v", missing, err)
			}
			for name, call := range entries {
				if err := call(ds); !errors.Is(err, ErrPayloadBackfillSchemaOutdated) {
					t.Fatalf("%s error = %v, want ErrPayloadBackfillSchemaOutdated", name, err)
				}
			}
		})
	}
}

// Per-table high-waters: advancing events.rowid well past command_audits must
// not put later audit inserts under a shared ceiling. An audit row created
// above the audit start ceiling (but below the events ceiling) is left for a
// later run, and this run still reaches completed.
func TestPayloadBackfillPerTableHighWaterIgnoresLaterAuditInserts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, path := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	// Force events.rowid far above any audit rowid.
	insertPlaintextEventAtRowID(t, db, 1000, eventSeed{
		ID: "hw-event-high", Kind: "note", Body: compressibleBody("event-high"),
	})
	insertPlaintextEvent(t, db, eventSeed{ID: "hw-audit-host", Kind: "command_executed", Body: ""})
	insertPlaintextAuditAtRowID(t, db, 1, auditSeed{
		EventID: "hw-audit-host",
		Command: compressibleBody("audit-at-1"),
		Input:   compressibleBody("audit-in-1"),
		Output:  compressibleBody("audit-out-1"),
	})

	var inserted bool
	ds.onBeforeCommitBatch = func([]backfillCandidate) {
		if inserted {
			return
		}
		inserted = true
		side, err := sql.Open("sqlite", directSQLiteRWDSN(path))
		if err != nil {
			t.Fatalf("open side conn: %v", err)
		}
		defer func() { _ = side.Close() }()
		// Rowid 500 is below the events ceiling (1000) but above the audit
		// ceiling (1). A shared max would have processed this insert.
		insertPlaintextEvent(t, side, eventSeed{ID: "hw-late-host", Kind: "command_executed", Body: ""})
		insertPlaintextAuditAtRowID(t, side, 500, auditSeed{
			EventID: "hw-late-host",
			Command: compressibleBody("late-audit"),
			Input:   compressibleBody("late-in"),
			Output:  compressibleBody("late-out"),
		})
	}

	result, err := ds.Run(ctx, defaultBackfillConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillCompleted), result.State); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
	assertAuditCodec(t, db, "hw-audit-host", "command", payloadCodecZstd)
	// Late insert was plaintext and stays unprocessed: outside the audit ceiling.
	var lateCodec sql.NullString
	if err := db.QueryRow(`SELECT command_codec FROM command_audits WHERE event_id = ?`, "hw-late-host").Scan(&lateCodec); err != nil {
		t.Fatalf("read late audit codec: %v", err)
	}
	if lateCodec.Valid {
		t.Fatalf("late audit command_codec = %q, want NULL (not processed by this run)", lateCodec.String)
	}
	var auditHigh int64
	if err := db.QueryRow(`SELECT audit_high_water_rowid FROM payload_backfill_runs WHERE run_id = ?`, result.RunID).Scan(&auditHigh); err != nil {
		t.Fatalf("read audit high water: %v", err)
	}
	if auditHigh != 1 {
		t.Fatalf("audit_high_water_rowid = %d, want 1", auditHigh)
	}
}

// The same numeric rowid present in both tables before the run is processed
// once per lane against the correct table.
func TestPayloadBackfillSameRowIDPresentBeforeRunIsProcessedPerTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	const sharedRowID int64 = 42
	insertPlaintextEventAtRowID(t, db, sharedRowID, eventSeed{
		ID: "same-rowid-event", Kind: "note", Body: compressibleBody("event-42"),
	})
	insertPlaintextEvent(t, db, eventSeed{ID: "same-rowid-audit-host", Kind: "command_executed", Body: ""})
	insertPlaintextAuditAtRowID(t, db, sharedRowID, auditSeed{
		EventID: "same-rowid-audit-host",
		Command: compressibleBody("audit-42"),
		Input:   compressibleBody("audit-42-in"),
		Output:  compressibleBody("audit-42-out"),
	})

	result, err := ds.Run(ctx, defaultBackfillConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillCompleted), result.State); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
	assertBodyCodec(t, db, "same-rowid-event", payloadCodecZstd)
	assertAuditCodec(t, db, "same-rowid-audit-host", "command", payloadCodecZstd)
	assertAuditCodec(t, db, "same-rowid-audit-host", "input", payloadCodecZstd)
	assertAuditCodec(t, db, "same-rowid-audit-host", "output", payloadCodecZstd)
	// One body + three audit fields for the shared rowid.
	if result.EncodedRows < 4 {
		t.Fatalf("encoded_rows = %d, want >= 4 (body + 3 audit fields)", result.EncodedRows)
	}
	gotEvent := readDecodedBody(t, db, "same-rowid-event")
	if diff := cmp.Diff([]byte(compressibleBody("event-42")), gotEvent); diff != "" {
		t.Fatalf("decoded event body mismatch (-want +got):\n%s", diff)
	}
	gotAudit := readDecodedAudit(t, db, "same-rowid-audit-host")
	if diff := cmp.Diff(compressibleBody("audit-42"), gotAudit.Command); diff != "" {
		t.Fatalf("decoded audit command mismatch (-want +got):\n%s", diff)
	}
}

// Expansion bounds each lane by its own ceiling: an events.rowid in the pick
// set must not pull a same-numbered command_audits row created after the audit
// ceiling was fixed. Without the audit-arm ceiling, the late insert rides in
// on the events.rowid and is rewritten by this run.
func TestPayloadBackfillSameRowIDAboveAuditCeilingIsNotLoaded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, path := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	const sharedRowID int64 = 500
	// A low row so the first batch commits before the shared rowid is selected;
	// the late audit is inserted then, above the audit ceiling of 1.
	insertPlaintextEventAtRowID(t, db, 1, eventSeed{
		ID: "ceiling-early", Kind: "note", Body: compressibleBody("early"),
	})
	insertPlaintextEventAtRowID(t, db, sharedRowID, eventSeed{
		ID: "ceiling-event-500", Kind: "note", Body: compressibleBody("event-500"),
	})
	insertPlaintextEvent(t, db, eventSeed{ID: "ceiling-audit-host-1", Kind: "command_executed", Body: ""})
	insertPlaintextAuditAtRowID(t, db, 1, auditSeed{
		EventID: "ceiling-audit-host-1",
		Command: compressibleBody("audit-at-1"),
		Input:   compressibleBody("audit-in-1"),
		Output:  compressibleBody("audit-out-1"),
	})

	var inserted bool
	ds.onBeforeCommitBatch = func([]backfillCandidate) {
		if inserted {
			return
		}
		inserted = true
		side, err := sql.Open("sqlite", directSQLiteRWDSN(path))
		if err != nil {
			t.Fatalf("open side conn: %v", err)
		}
		defer func() { _ = side.Close() }()
		// Host for the late audit: auto rowid lands past the events ceiling.
		insertPlaintextEvent(t, side, eventSeed{ID: "ceiling-late-host", Kind: "command_executed", Body: ""})
		insertPlaintextAuditAtRowID(t, side, sharedRowID, auditSeed{
			EventID: "ceiling-late-host",
			Command: compressibleBody("late-audit-500"),
			Input:   compressibleBody("late-in-500"),
			Output:  compressibleBody("late-out-500"),
		})
	}

	result, err := ds.Run(ctx, apptypes.PayloadBackfillConfig{BatchRows: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillCompleted), result.State); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
	assertBodyCodec(t, db, "ceiling-event-500", payloadCodecZstd)
	// Late audit at rowid 500 must stay unprocessed: above the audit ceiling.
	var lateCodec sql.NullString
	if err := db.QueryRow(`SELECT command_codec FROM command_audits WHERE event_id = ?`, "ceiling-late-host").Scan(&lateCodec); err != nil {
		t.Fatalf("read late audit codec: %v", err)
	}
	if lateCodec.Valid {
		t.Fatalf("late audit command_codec = %q, want NULL (loader must not pull it via events.rowid %d)", lateCodec.String, sharedRowID)
	}
	var auditHigh int64
	if err := db.QueryRow(`SELECT audit_high_water_rowid FROM payload_backfill_runs WHERE run_id = ?`, result.RunID).Scan(&auditHigh); err != nil {
		t.Fatalf("read audit high water: %v", err)
	}
	if auditHigh != 1 {
		t.Fatalf("audit_high_water_rowid = %d, want 1", auditHigh)
	}
}

// Empty selector result with eligible work still past the cursor must not
// report completed. onSelectedBatch forces the empty result without duplicating
// the candidate SQL.
func TestPayloadBackfillDoesNotCompleteWhenPickExpandsEmptyWithRemainingWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	for _, id := range []string{"split-early-1", "split-early-2", "split-late"} {
		insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: compressibleBody(id)})
	}

	var forcedEmpty bool
	ds.onSelectedBatch = func(batch []backfillCandidate) []backfillCandidate {
		if forcedEmpty || len(batch) == 0 {
			return batch
		}
		forcedEmpty = true
		return nil
	}

	result, err := ds.Run(ctx, apptypes.PayloadBackfillConfig{BatchRows: 2})
	if err == nil {
		t.Fatalf("Run err = nil, want remaining-work error; state=%q", result.State)
	}
	if !strings.Contains(err.Error(), "eligible fields remain past cursor") {
		t.Fatalf("error = %v, want remaining-work condition naming eligible fields past cursor", err)
	}
	if result.State == string(apptypes.PayloadBackfillCompleted) {
		t.Fatalf("state = completed; empty selection must not finish the run while eligible work remains")
	}
	// Fail-safe leaves the run resumable (running when the error is not a cancel).
	var state string
	if err := db.QueryRow(`SELECT state FROM payload_backfill_runs`).Scan(&state); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if !apptypes.PayloadBackfillState(state).CanResume() {
		t.Fatalf("persisted state = %q, want resumable (running or paused)", state)
	}
	// Seeded rows must still be eligible — never silently skipped.
	assertBodyCodec(t, db, "split-late", payloadCodecIdentity)
}

// Resume must keep the ceilings fixed at run start. Growing either table while
// paused must not move the checkpointed high-waters or process the new rows.
func TestPayloadBackfillResumePreservesBothCeilings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	// Enough rows that StopAfterBatches=1 leaves work under the original ceilings.
	for _, id := range []string{"resume-e1", "resume-e2", "resume-e3", "resume-e4"} {
		insertIdentityEvent(t, db, eventSeed{ID: id, Kind: "note", Body: compressibleBody(id)})
	}
	insertPlaintextEvent(t, db, eventSeed{ID: "resume-a1-host", Kind: "command_executed", Body: ""})
	insertPlaintextAudit(t, db, auditSeed{
		EventID: "resume-a1-host",
		Command: compressibleBody("resume-a1"),
		Input:   compressibleBody("resume-a1-in"),
		Output:  compressibleBody("resume-a1-out"),
	})
	insertPlaintextEvent(t, db, eventSeed{ID: "resume-a2-host", Kind: "command_executed", Body: ""})
	insertPlaintextAudit(t, db, auditSeed{
		EventID: "resume-a2-host",
		Command: compressibleBody("resume-a2"),
		Input:   compressibleBody("resume-a2-in"),
		Output:  compressibleBody("resume-a2-out"),
	})

	var wantEventHigh, wantAuditHigh int64
	if err := db.QueryRow(`SELECT MAX(rowid) FROM events`).Scan(&wantEventHigh); err != nil {
		t.Fatalf("read events max: %v", err)
	}
	if err := db.QueryRow(`SELECT MAX(rowid) FROM command_audits`).Scan(&wantAuditHigh); err != nil {
		t.Fatalf("read audits max: %v", err)
	}

	cfg := defaultBackfillConfig()
	cfg.BatchRows = 2
	cfg.StopAfterBatches = 1
	paused, err := ds.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run (pause): %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillPaused), paused.State); diff != "" {
		t.Fatalf("paused state mismatch (-want +got):\n%s", diff)
	}

	// Grow both tables past the recorded ceilings while paused.
	const pastEventRowID int64 = 5000
	const pastAuditRowID int64 = 5000
	if pastEventRowID <= wantEventHigh || pastAuditRowID <= wantAuditHigh {
		t.Fatalf("test ceiling constants too low: eventHigh=%d auditHigh=%d", wantEventHigh, wantAuditHigh)
	}
	insertPlaintextEventAtRowID(t, db, pastEventRowID, eventSeed{
		ID: "resume-late-event", Kind: "note", Body: compressibleBody("late-event"),
	})
	insertPlaintextEvent(t, db, eventSeed{ID: "resume-late-audit-host", Kind: "command_executed", Body: ""})
	insertPlaintextAuditAtRowID(t, db, pastAuditRowID, auditSeed{
		EventID: "resume-late-audit-host",
		Command: compressibleBody("late-audit"),
		Input:   compressibleBody("late-audit-in"),
		Output:  compressibleBody("late-audit-out"),
	})

	cfg.StopAfterBatches = 0
	done, err := ds.Resume(ctx, cfg)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if diff := cmp.Diff(string(apptypes.PayloadBackfillCompleted), done.State); diff != "" {
		t.Fatalf("completed state mismatch (-want +got):\n%s", diff)
	}

	var gotEventHigh, gotAuditHigh int64
	if err := db.QueryRow(`
		SELECT high_water_rowid, audit_high_water_rowid FROM payload_backfill_runs WHERE run_id = ?`,
		done.RunID,
	).Scan(&gotEventHigh, &gotAuditHigh); err != nil {
		t.Fatalf("read checkpointed ceilings: %v", err)
	}
	if diff := cmp.Diff(wantEventHigh, gotEventHigh); diff != "" {
		t.Fatalf("high_water_rowid mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantAuditHigh, gotAuditHigh); diff != "" {
		t.Fatalf("audit_high_water_rowid mismatch (-want +got):\n%s", diff)
	}

	// Rows added while paused stay outside the walk.
	var lateEventCodec sql.NullString
	if err := db.QueryRow(`SELECT body_codec FROM events WHERE id = ?`, "resume-late-event").Scan(&lateEventCodec); err != nil {
		t.Fatalf("read late event codec: %v", err)
	}
	if lateEventCodec.Valid {
		t.Fatalf("late event body_codec = %q, want NULL (above checkpointed events ceiling)", lateEventCodec.String)
	}
	var lateAuditCodec sql.NullString
	if err := db.QueryRow(`SELECT command_codec FROM command_audits WHERE event_id = ?`, "resume-late-audit-host").Scan(&lateAuditCodec); err != nil {
		t.Fatalf("read late audit codec: %v", err)
	}
	if lateAuditCodec.Valid {
		t.Fatalf("late audit command_codec = %q, want NULL (above checkpointed audit ceiling)", lateAuditCodec.String)
	}
	// In-ceiling rows finished.
	assertBodyCodec(t, db, "resume-e1", payloadCodecZstd)
	assertAuditCodec(t, db, "resume-a1-host", "command", payloadCodecZstd)
}

// Identity audit values are bound as TEXT, matching every other writer. Nothing
// currently runs LIKE against the audit columns, so this pins the invariant
// rather than fixing a live break (see #1685 for the events.body LIKE failure
// when an identity value was stored as BLOB).
func TestPayloadBackfillPreservesTextAffinityForIncompressibleAuditCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, ds, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	// High-entropy bytes zstd cannot shrink — recipe keeps identity.
	raw := make([]byte, 256)
	for i := range raw {
		raw[i] = byte(i)
	}
	const id = "audit-affinity"
	insertPlaintextEvent(t, db, eventSeed{ID: id, Kind: "command_executed", Body: ""})
	insertPlaintextAudit(t, db, auditSeed{
		EventID: id,
		Command: string(raw),
		Input:   "short-in",
		Output:  "short-out",
	})

	if _, err := ds.Run(ctx, defaultBackfillConfig()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertAuditCodec(t, db, id, "command", payloadCodecIdentity)
	got := readDecodedAudit(t, db, id)
	if diff := cmp.Diff(string(raw), got.Command); diff != "" {
		t.Fatalf("decoded command mismatch (-want +got):\n%s", diff)
	}
	stored := readStoredAudit(t, db, id)
	if diff := cmp.Diff(raw, stored.Command); diff != "" {
		t.Fatalf("stored command bytes mismatch (-want +got):\n%s", diff)
	}
	var storedType string
	if err := db.QueryRow(`SELECT typeof(command_text) FROM command_audits WHERE event_id = ?`, id).Scan(&storedType); err != nil {
		t.Fatalf("read command_text type: %v", err)
	}
	if diff := cmp.Diff("text", storedType); diff != "" {
		t.Fatalf("command_text affinity mismatch (-want +got):\n%s", diff)
	}
}

func insertPlaintextEventAtRowID(t *testing.T, db *sql.DB, rowID int64, seed eventSeed) {
	t.Helper()
	var sourceHook any
	if seed.SourceHook != "" {
		sourceHook = seed.SourceHook
	}
	if _, err := db.Exec(`
		INSERT INTO events(
			rowid, id, kind, client, agent, session_id, workspace, body, created_at, source_hook
		) VALUES (?, ?, ?, 'cli', 'codex', 'session-codec', 'ws-codec', ?, '2026-08-09T00:00:00Z', ?)`,
		rowID, seed.ID, seed.Kind, seed.Body, sourceHook,
	); err != nil {
		t.Fatalf("insert plaintext event %s at rowid %d: %v", seed.ID, rowID, err)
	}
}

// insertBackfillRunRow seeds a payload_backfill_runs row directly for
// loader-ordering tests that bypass the high-level API.
func insertBackfillRunRow(t *testing.T, db *sql.DB, runID, state, startedAt string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO payload_backfill_runs(
			run_id, recipe_version, high_water_rowid, audit_high_water_rowid,
			cursor_rowid, pass_count, state, worker_token, started_at, updated_at
		) VALUES (?, 'test-recipe', 0, 0, 0, 0, ?, '', ?, ?)`,
		runID, state, startedAt, startedAt,
	); err != nil {
		t.Fatalf("insert backfill run %s: %v", runID, err)
	}
}

// TestLoadActiveBackfillRun_PicksLaterInstantAcrossLexicalInversion verifies
// that the active-run loader returns the running row when a completed row has
// a lexically-later started_at. RFC3339Nano '.' (0x2E) < 'Z' (0x5A), so
// a sub-second timestamp sorts lexically before a whole-second timestamp of
// a later instant — the #1185 hazard. The fix (ts_norm ORDER BY) is
// defensive here because the unique partial index already limits active rows
// to one; this test documents correctness in the presence of the inversion.
func TestLoadActiveBackfillRun_PicksLaterInstantAcrossLexicalInversion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	// wholeSecond sorts lexically after subSecond ('Z' > '.'), but subSecond
	// represents the later instant.
	const (
		wholeSecond = "2024-01-01T12:00:00Z"
		subSecond   = "2024-01-01T12:00:00.5Z"
	)
	insertBackfillRunRow(t, db, "run-completed", "completed", wholeSecond)
	insertBackfillRunRow(t, db, "run-active", "running", subSecond)

	row, err := loadActiveBackfillRun(ctx, db)
	if err != nil {
		t.Fatalf("loadActiveBackfillRun: %v", err)
	}
	if diff := cmp.Diff("run-active", row.RunID); diff != "" {
		t.Fatalf("run_id mismatch (-want +got):\n%s", diff)
	}
}

// TestLoadLatestBackfillRun_PicksLaterInstantAcrossLexicalInversion is the
// red-before-green test for the ts_norm fix on loadLatestBackfillRun. Without
// ts_norm, ORDER BY started_at DESC returns the whole-second row first because
// 'Z' (0x5A) > '.' (0x2E) lexically; ts_norm normalises both to fixed-width
// nanosecond text so the temporally-later sub-second row wins.
func TestLoadLatestBackfillRun_PicksLaterInstantAcrossLexicalInversion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	const (
		wholeSecond = "2024-01-01T12:00:00Z"   // lexically later, temporally earlier
		subSecond   = "2024-01-01T12:00:00.5Z" // lexically earlier, temporally later
	)
	insertBackfillRunRow(t, db, "run-A", "completed", wholeSecond)
	insertBackfillRunRow(t, db, "run-B", "completed", subSecond)

	row, err := loadLatestBackfillRun(ctx, db)
	if err != nil {
		t.Fatalf("loadLatestBackfillRun: %v", err)
	}
	if diff := cmp.Diff("run-B", row.RunID); diff != "" {
		t.Fatalf("run_id mismatch (-want +got):\n%s", diff)
	}
}

// TestLoadLatestBackfillRun_TieBreakByRunID verifies the run_id DESC tie-break
// when two rows share an identical started_at value.
func TestLoadLatestBackfillRun_TieBreakByRunID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _, _ := openPayloadBackfillFixture(t)
	defer closePayloadBackfillFixture(t, db)

	const ts = "2024-01-01T12:00:00Z"
	insertBackfillRunRow(t, db, "run-1", "completed", ts)
	insertBackfillRunRow(t, db, "run-2", "completed", ts)

	row, err := loadLatestBackfillRun(ctx, db)
	if err != nil {
		t.Fatalf("loadLatestBackfillRun: %v", err)
	}
	// run_id DESC: "run-2" > "run-1" lexically.
	if diff := cmp.Diff("run-2", row.RunID); diff != "" {
		t.Fatalf("run_id tie-break mismatch (-want +got):\n%s", diff)
	}
}

func insertPlaintextAuditAtRowID(t *testing.T, db *sql.DB, rowID int64, seed auditSeed) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO command_audits(
			rowid, event_id, command_text, input_text, output_text,
			input_truncated, output_truncated, input_original_bytes, output_original_bytes,
			exit_code, failed
		) VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?, 0, 0)`,
		rowID, seed.EventID, seed.Command, seed.Input, seed.Output,
		seed.InputOriginalBytes, seed.OutputOriginalBytes,
	); err != nil {
		t.Fatalf("insert plaintext audit %s at rowid %d: %v", seed.EventID, rowID, err)
	}
}
