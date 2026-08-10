package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// TestBodyCodecDerivationTriggers pins migration 053: body_stored_bytes and
// legacy_source_hook must survive a representation change to a non-identity
// codec, while plaintext/identity content changes must still re-derive.
func TestBodyCodecDerivationTriggers(t *testing.T) {
	t.Parallel()

	t.Run("provenance survives re-encoding", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id   = "prov-reencode"
			body = "plaintext provenance body"
		)
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "note", Body: body, SourceHook: "stop",
		})
		wantStored := int64(len(body))
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)

		reencodeBodyToZstd(t, db, id, []byte(body))

		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
	})

	t.Run("hook survives re-encoding", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id   = "hook-subagent"
			body = "[phase:subagent] complete"
		)
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "session_ended", Body: body,
		})
		assertLegacySourceHook(t, db, id, "subagent_stop")
		wantStored := int64(len(body))
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)

		reencodeBodyToZstd(t, db, id, []byte(body))

		assertLegacySourceHook(t, db, id, "subagent_stop")
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
	})

	t.Run("pre-compact hook survives re-encoding", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id   = "hook-precompact"
			body = "[phase:pre-compact] summary"
		)
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "compact_summary", Body: body,
		})
		assertLegacySourceHook(t, db, id, "pre_compact")
		wantStored := int64(len(body))

		reencodeBodyToZstd(t, db, id, []byte(body))

		assertLegacySourceHook(t, db, id, "pre_compact")
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
	})

	t.Run("plaintext content change still re-derives", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id      = "plain-content"
			oldBody = "[phase:subagent] before"
			newBody = "different plaintext after"
		)
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "session_ended", Body: oldBody,
		})
		assertLegacySourceHook(t, db, id, "subagent_stop")
		assertBodyStoredBytes(t, db, id, int64(len(oldBody)), int64(len(oldBody)))

		if _, err := db.Exec(`UPDATE events SET body = ? WHERE id = ?`, newBody, id); err != nil {
			t.Fatalf("update plaintext body: %v", err)
		}

		assertBodyStoredBytes(t, db, id, int64(len(newBody)), int64(len(newBody)))
		assertLegacySourceHook(t, db, id, "")
	})

	t.Run("identity content change still re-derives", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id      = "identity-content"
			oldBody = "[phase:subagent] identity before"
			newBody = "[phase:subagent] identity after longer"
		)
		insertIdentityEvent(t, db, eventSeed{
			ID: id, Kind: "session_ended", Body: oldBody,
		})
		assertLegacySourceHook(t, db, id, "subagent_stop")
		assertBodyStoredBytes(t, db, id, int64(len(oldBody)), int64(len(oldBody)))

		if _, err := db.Exec(`UPDATE events SET body = ? WHERE id = ?`, newBody, id); err != nil {
			t.Fatalf("update identity body: %v", err)
		}

		assertBodyStoredBytes(t, db, id, int64(len(newBody)), int64(len(newBody)))
		assertLegacySourceHook(t, db, id, "subagent_stop")
	})

	t.Run("retention blank preserves provenance", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id   = "retention-blank"
			body = "available body to prune"
		)
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "note", Body: body, SourceHook: "stop",
		})
		wantStored := int64(len(body))
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)

		if _, err := db.Exec(`
			UPDATE events
			   SET body = ?,
			       body_availability = 'unavailable_retention',
			       body_pruned_at = '2026-08-09T00:00:00Z',
			       body_pruned_plan_id = 'codec-deriv-plan'
			 WHERE id = ?
		`, domtypes.EventBodyUnavailableRetentionMarker, id); err != nil {
			t.Fatalf("blank body for retention: %v", err)
		}

		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
		var availability string
		if err := db.QueryRow(`SELECT body_availability FROM events WHERE id = ?`, id).Scan(&availability); err != nil {
			t.Fatalf("read body_availability: %v", err)
		}
		if diff := cmp.Diff("unavailable_retention", availability); diff != "" {
			t.Fatalf("body_availability mismatch (-want +got):\n%s", diff)
		}
	})

	// Pins that provenance does not depend on 026 chain-firing the projection
	// update a second time: with no projection row, VALUES alone must still
	// write the plaintext body_stored_bytes for a non-identity re-encode.
	t.Run("provenance survives re-encoding without projection row", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const (
			id   = "prov-no-projection"
			body = "plaintext without projection"
		)
		insertPlaintextEvent(t, db, eventSeed{
			ID: id, Kind: "note", Body: body, SourceHook: "stop",
		})
		wantStored := int64(len(body))
		assertBodyStoredBytes(t, db, id, wantStored, wantStored)

		if _, err := db.Exec(`DELETE FROM event_metadata_projection WHERE id = ?`, id); err != nil {
			t.Fatalf("delete projection row: %v", err)
		}
		var projectionCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM event_metadata_projection WHERE id = ?`, id).Scan(&projectionCount); err != nil {
			t.Fatalf("count projection rows: %v", err)
		}
		if projectionCount != 0 {
			t.Fatalf("projection row still present after delete")
		}

		reencodeBodyToZstd(t, db, id, []byte(body))

		assertBodyStoredBytes(t, db, id, wantStored, wantStored)
	})

	t.Run("nonidentity insert preserves provenance and skips hook inference", func(t *testing.T) {
		t.Parallel()
		db := openBodyCodecDerivationDB(t)
		defer closeBodyCodecDerivationDB(t, db)

		const id = "zstd-insert"
		body := []byte("[phase:subagent] " + compressibleBody("insert"))
		encoded, err := encodePayload(body, payloadCodecZstd)
		if err != nil {
			t.Fatalf("encode zstd payload: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO events(
				id, kind, client, agent, session_id, workspace, body, created_at,
				body_stored_bytes, body_codec, body_format_version, body_plaintext_bytes,
				body_encoded_bytes, body_sha256
			) VALUES (?, 'session_ended', 'cli', 'codex', 'session-codec', 'ws-codec', ?,
				'2026-08-09T00:00:00Z', ?, ?, ?, ?, ?, ?)
		`, id, encoded.Bytes, len(body), encoded.Codec, encoded.FormatVersion,
			encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256); err != nil {
			t.Fatalf("insert zstd event: %v", err)
		}

		assertBodyStoredBytes(t, db, id, int64(len(body)), int64(len(body)))
		assertLegacySourceHook(t, db, id, "")
	})
}

type eventSeed struct {
	ID         string
	Kind       string
	Body       string
	SourceHook string // empty → NULL
}

func openBodyCodecDerivationDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "codec-derivation.db")
	if err := NewStoreManagementDatasource(NewDatabase(path, preparedMigrations(t))).Initialize(ctx); err != nil {
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
	return db
}

func closeBodyCodecDerivationDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

func insertPlaintextEvent(t *testing.T, db *sql.DB, seed eventSeed) {
	t.Helper()
	var sourceHook any
	if seed.SourceHook != "" {
		sourceHook = seed.SourceHook
	}
	if _, err := db.Exec(`
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at, source_hook
		) VALUES (?, ?, 'cli', 'codex', 'session-codec', 'ws-codec', ?, '2026-08-09T00:00:00Z', ?)
	`, seed.ID, seed.Kind, seed.Body, sourceHook); err != nil {
		t.Fatalf("insert plaintext event %s: %v", seed.ID, err)
	}
}

func insertIdentityEvent(t *testing.T, db *sql.DB, seed eventSeed) {
	t.Helper()
	var sourceHook any
	if seed.SourceHook != "" {
		sourceHook = seed.SourceHook
	}
	plaintext := []byte(seed.Body)
	encoded, err := encodePayload(plaintext, payloadCodecIdentity)
	if err != nil {
		t.Fatalf("encode identity payload: %v", err)
	}
	// Bind as TEXT via string(...), matching event_delivery_store (production
	// identity writes). A raw []byte becomes a BLOB and breaks LIKE inference.
	if _, err := db.Exec(`
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
			body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		) VALUES (?, ?, 'cli', 'codex', 'session-codec', 'ws-codec', ?, '2026-08-09T00:00:00Z', ?,
		          ?, ?, ?, ?, ?)
	`, seed.ID, seed.Kind, string(encoded.Bytes), sourceHook,
		encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256); err != nil {
		t.Fatalf("insert identity event %s: %v", seed.ID, err)
	}
}

func reencodeBodyToZstd(t *testing.T, db *sql.DB, id string, plaintext []byte) {
	t.Helper()
	encoded, err := encodePayload(plaintext, payloadCodecZstd)
	if err != nil {
		t.Fatalf("encode zstd payload: %v", err)
	}
	// Require a size change so a silent overwrite of body_stored_bytes is visible.
	if encoded.StoredBytes == int64(len(plaintext)) {
		t.Fatalf("zstd did not shrink body (%d → %d); test cannot detect provenance corruption", len(plaintext), encoded.StoredBytes)
	}
	if _, err := db.Exec(`
		UPDATE events
		   SET body = ?,
		       body_codec = ?,
		       body_format_version = ?,
		       body_plaintext_bytes = ?,
		       body_encoded_bytes = ?,
		       body_sha256 = ?
		 WHERE id = ?
	`, encoded.Bytes, encoded.Codec, encoded.FormatVersion, encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256, id); err != nil {
		t.Fatalf("re-encode body to zstd: %v", err)
	}
}

type bodyStoredBytesState struct {
	Events     int64
	Projection int64
}

func readBodyStoredBytes(t *testing.T, db *sql.DB, id string) bodyStoredBytesState {
	t.Helper()
	var state bodyStoredBytesState
	if err := db.QueryRow(`
		SELECT events.body_stored_bytes, event_metadata_projection.body_stored_bytes
		  FROM events
		  JOIN event_metadata_projection ON event_metadata_projection.id = events.id
		 WHERE events.id = ?
	`, id).Scan(&state.Events, &state.Projection); err != nil {
		t.Fatalf("read body_stored_bytes for %s: %v", id, err)
	}
	return state
}

func assertBodyStoredBytes(t *testing.T, db *sql.DB, id string, wantEvents, wantProjection int64) {
	t.Helper()
	got := readBodyStoredBytes(t, db, id)
	want := bodyStoredBytesState{Events: wantEvents, Projection: wantProjection}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("body_stored_bytes mismatch for %s (-want +got):\n%s", id, diff)
	}
}

func assertLegacySourceHook(t *testing.T, db *sql.DB, id, want string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRow(`
		SELECT legacy_source_hook FROM event_metadata_projection WHERE id = ?
	`, id).Scan(&got); err != nil {
		t.Fatalf("read legacy_source_hook for %s: %v", id, err)
	}
	gotValue := ""
	if got.Valid {
		gotValue = got.String
	}
	if diff := cmp.Diff(want, gotValue); diff != "" {
		t.Fatalf("legacy_source_hook mismatch for %s (-want +got):\n%s", id, diff)
	}
}
