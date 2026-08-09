package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

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
	})
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
