package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestRawBodyRetention_snapshotReportsEncodedExtents(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	compressedBody := string(bytes.Repeat([]byte("compressible body "), 4096))
	fixtures := []struct {
		name string
		id   string
		body string
	}{
		{name: "compressed", id: "retention-compressed", body: compressedBody},
		{name: "legacy UTF-8", id: "retention-legacy", body: "日本語"},
	}
	for _, fixture := range fixtures {
		if err := events.Save(context.Background(), rawBodyRetentionEvent(t, fixture.id, fixture.body, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))); err != nil {
			t.Fatalf("Save(%s) error = %v", fixture.name, err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE events
SET body=?, body_codec=NULL, body_format_version=NULL, body_plaintext_bytes=NULL,
    body_encoded_bytes=NULL, body_sha256=NULL
WHERE id='retention-legacy'`, "日本語"); err != nil {
		t.Fatalf("make legacy event: %v", err)
	}
	_ = db.Close()
	makeRawBodyRetentionEligible(t, dbPath, "retention-compressed", "retention-legacy")

	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	tests := []struct {
		name           string
		id             string
		encodedBytes   int
		plaintextBytes int
	}{
		{name: "compressed candidate", id: "retention-compressed", encodedBytes: 44, plaintextBytes: len([]byte(compressedBody))},
		{name: "legacy UTF-8 candidate", id: "retention-legacy", encodedBytes: len([]byte("日本語")), plaintextBytes: len([]byte("日本語"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got struct {
				ID             string
				EncodedBytes   int
				PlaintextBytes int
			}
			for _, candidate := range snapshot.Candidates {
				if candidate.EventID == test.id {
					got = struct {
						ID             string
						EncodedBytes   int
						PlaintextBytes int
					}{candidate.EventID, candidate.EncodedBytes, candidate.PlaintextBytes}
				}
			}
			want := struct {
				ID             string
				EncodedBytes   int
				PlaintextBytes int
			}{test.id, test.encodedBytes, test.plaintextBytes}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("candidate extent mismatch (-want +got):\n%s", diff)
			}
		})
	}
	total := 0
	for _, candidate := range snapshot.Candidates {
		total += candidate.EncodedBytes
	}
	wantTotal := 44 + len([]byte("日本語"))
	if diff := cmp.Diff(wantTotal, total); diff != "" {
		t.Fatalf("encoded reclaim total mismatch (-want +got):\n%s", diff)
	}
}

func TestRawBodyRetention_applyRetryAndRestore(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	event := rawBodyRetentionEvent(t, "retention-event", "body to recover", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())
	beforeMetadata := rawBodyMetadataRow(t, dbPath, event.EventID().String())

	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	if len(snapshot.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(snapshot.Candidates))
	}
	planID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ApplyRawBodyPlan() error = %v", err)
	}
	if result.PrunedCount != 1 || result.AlreadyPruned != 0 {
		t.Fatalf("apply result = %+v", result)
	}
	if after := rawBodyMetadataRow(t, dbPath, event.EventID().String()); after != beforeMetadata {
		t.Fatalf("metadata changed after prune:\nbefore=%q\nafter=%q", beforeMetadata, after)
	}
	details, err := events.GetDetails(context.Background(), event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if details.Event().BodyAvailability() != types.BodyAvailabilityUnavailableRetention || details.Event().Body() != "" {
		t.Fatalf("pruned event availability=%q body=%q", details.Event().BodyAvailability(), details.Event().Body())
	}
	matches, err := events.Search(context.Background(), types.EventBodyUnavailableRetentionMarker, "", "", "", "", "", time.Time{}, time.Time{}, 10, 0, false)
	if err != nil {
		t.Fatalf("Search(retention marker) error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("Search(retention marker) returned %d pruned events, want 0", len(matches))
	}

	retry, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Date(2026, 7, 2, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("retry ApplyRawBodyPlan() error = %v", err)
	}
	if retry.PrunedCount != 0 || retry.AlreadyPruned != 1 {
		t.Fatalf("retry result = %+v", retry)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE sessions SET ended_at = NULL WHERE session_id = 'retention-session'`); err != nil {
		t.Fatalf("activate session before retry: %v", err)
	}
	_ = db.Close()
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Date(2026, 7, 2, 2, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("retry ApplyRawBodyPlan() error = %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open(cleanup) error = %v", err)
	}
	if _, err := db.Exec(`UPDATE sessions SET ended_at = '2026-06-02T00:00:00Z' WHERE session_id = 'retention-session'`); err != nil {
		t.Fatalf("remove active session: %v", err)
	}
	_ = db.Close()

	recovery := []apptypes.RawBodyRecoveryBody{{Candidate: snapshot.Candidates[0], Body: event.Body()}}
	restored, err := store.RestoreRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, recovery, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RestoreRawBodyPlan() error = %v", err)
	}
	if restored.RestoredCount != 1 {
		t.Fatalf("restore result = %+v", restored)
	}
	details, err = events.GetDetails(context.Background(), event.EventID())
	if err != nil {
		t.Fatalf("GetDetails(restored) error = %v", err)
	}
	if !details.Event().BodyAvailability().IsAvailable() || details.Event().Body() != event.Body() {
		t.Fatalf("restored event availability=%q body=%q", details.Event().BodyAvailability(), details.Event().Body())
	}
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("ApplyRawBodyPlan() after restore error = nil, want terminal-plan rejection")
	}
}

func TestRawBodyRetention_planSnapshotDoesNotChangeDatabaseBytes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := events.Save(context.Background(), rawBodyRetentionEvent(t, "read-only-plan", "body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, "read-only-plan")
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("ReadFile(before) error = %v", err)
	}
	beforeInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Stat(before) error = %v", err)
	}
	if _, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("ReadFile(after) error = %v", err)
	}
	afterInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Stat(after) error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("retention plan snapshot changed database bytes")
	}
	if !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("database mtime changed: before=%s after=%s", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

func TestRawBodyRetention_rejectsStaleCandidateWithoutPruning(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	event := rawBodyRetentionEvent(t, "stale-event", "original", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET body = 'changed', body_codec=NULL, body_format_version=NULL, body_plaintext_bytes=NULL, body_encoded_bytes=NULL, body_sha256=NULL WHERE id = 'stale-event'`); err != nil {
		t.Fatalf("mutate body: %v", err)
	}
	_ = db.Close()

	_, err = store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", snapshot.Candidates, time.Now().UTC())
	if err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want stale-plan rejection")
	}
	details, err := events.GetDetails(context.Background(), event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if !details.Event().BodyAvailability().IsAvailable() || details.Event().Body() != "changed" {
		t.Fatalf("stale apply changed event: availability=%q body=%q", details.Event().BodyAvailability(), details.Event().Body())
	}
}

func TestRawBodyRetention_rejectsReencodedCandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "encoded extent changed", body: "body whose representation changes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "traceary.db")
			events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
			if err := store.Initialize(context.Background()); err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			const eventID = "reencoded-event"
			if err := events.Save(context.Background(), rawBodyRetentionEvent(t, eventID, test.body, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			makeRawBodyRetentionEligible(t, dbPath, eventID)
			snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("ListRawBodyCandidates() error = %v", err)
			}
			before := snapshot.Candidates[0].EncodedBytes
			db, err := sql.Open("sqlite", "file:"+dbPath)
			if err != nil {
				t.Fatalf("sql.Open() error = %v", err)
			}
			if _, err := db.Exec(`UPDATE events SET body_encoded_bytes = body_encoded_bytes + 1 WHERE id = ?`, eventID); err != nil {
				t.Fatalf("simulate re-encode: %v", err)
			}
			_ = db.Close()

			_, err = store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", snapshot.Candidates, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
			if err == nil {
				t.Fatal("ApplyRawBodyPlan() error = nil, want encoded-extent stale-plan rejection")
			}
			if diff := cmp.Diff(before+1, rawBodyEncodedBytes(t, dbPath, eventID)); diff != "" {
				t.Fatalf("encoded extent after simulated re-encode mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRawBodyRetention_rejectsSchemaDriftWithoutPruning(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	event := rawBodyRetentionEvent(t, "schema-drift-event", "protected", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE retention_schema_drift(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create schema drift: %v", err)
	}
	_ = db.Close()

	_, err = store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", snapshot.Candidates, time.Now().UTC())
	if err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want schema-drift rejection")
	}
	details, err := events.GetDetails(context.Background(), event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if !details.Event().BodyAvailability().IsAvailable() || details.Event().Body() != "protected" {
		t.Fatalf("schema-drift apply changed event: availability=%q body=%q", details.Event().BodyAvailability(), details.Event().Body())
	}
}

func TestRawBodyRetention_excludesActiveSessionBodies(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at, ended_at, client, agent, workspace) VALUES ('retention-session', '2026-05-01T00:00:00Z', NULL, 'cli', 'codex', 'repo')`); err != nil {
		t.Fatalf("insert active session: %v", err)
	}
	_ = db.Close()
	event := rawBodyRetentionEvent(t, "active-event", "protected", time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	if len(snapshot.Candidates) != 0 || len(snapshot.Excluded) != 1 || snapshot.Excluded[0].EventID != "active-event" || snapshot.Excluded[0].Reason != types.RawBodyExclusionReasonSessionActive {
		t.Fatalf("snapshot candidates=%+v exclusions=%+v", snapshot.Candidates, snapshot.Excluded)
	}
}

func TestRawBodyRetention_rejectsSessionActivatedAfterPlan(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	event := rawBodyRetentionEvent(t, "activated-after-plan", "protected", time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE sessions SET ended_at = NULL WHERE session_id = 'retention-session'`); err != nil {
		t.Fatalf("activate session after plan: %v", err)
	}
	_ = db.Close()

	_, err = store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", snapshot.Candidates, time.Now().UTC())
	if err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want active-session stale-plan rejection")
	}
	details, err := events.GetDetails(context.Background(), event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if !details.Event().BodyAvailability().IsAvailable() || details.Event().Body() != "protected" {
		t.Fatalf("active-session rejection changed event: availability=%q body=%q", details.Event().BodyAvailability(), details.Event().Body())
	}
}

func TestRawBodyRetention_rejectsPlanWhenRefinementIsRemoved(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	event := rawBodyRetentionEvent(t, "refinement-removed", "protected", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, dbPath, event.EventID().String())
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`DELETE FROM session_refinements WHERE session_id = 'retention-session'`); err != nil {
		t.Fatalf("delete refinement: %v", err)
	}
	_ = db.Close()
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, "abababababababababababababababababababababababababababababababab", snapshot.Candidates, time.Now().UTC()); err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want stale-plan rejection")
	}
	details, err := events.GetDetails(context.Background(), event.EventID())
	if err != nil {
		t.Fatalf("GetDetails() error = %v", err)
	}
	if !details.Event().BodyAvailability().IsAvailable() {
		t.Fatalf("availability = %q, want available", details.Event().BodyAvailability())
	}
}

func TestRawBodyRetention_interruptionResumesFromDurableBatch(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	for index, id := range []string{"interrupt-a", "interrupt-b"} {
		event := rawBodyRetentionEvent(t, id, "payload-"+id, time.Date(2026, 5, 1, index, 0, 0, 0, time.UTC))
		if err := events.Save(context.Background(), event); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}
	makeRawBodyRetentionEligible(t, dbPath, "interrupt-a", "interrupt-b")
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	planID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	store.SetRawBodyPrunedHookForTest(func(int) error { return errors.New("injected interruption") })
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Now().UTC()); err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want interruption")
	}
	for index, candidate := range snapshot.Candidates {
		details, err := events.GetDetails(context.Background(), types.EventID(candidate.EventID))
		if err != nil {
			t.Fatalf("GetDetails(%s) error = %v", candidate.EventID, err)
		}
		wantAvailable := index != 0
		if details.Event().BodyAvailability().IsAvailable() != wantAvailable {
			t.Fatalf("event %s availability = %q, want available=%t after interruption", candidate.EventID, details.Event().BodyAvailability(), wantAvailable)
		}
	}
	store.SetRawBodyPrunedHookForTest(nil)
	result, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Now().UTC())
	if err != nil {
		t.Fatalf("retry ApplyRawBodyPlan() error = %v", err)
	}
	if result.PrunedCount != len(snapshot.Candidates)-1 || result.AlreadyPruned != 1 {
		t.Fatalf("retry result = %+v, want one resumed and one already durable", result)
	}
}

func TestRawBodyRetention_partialExecutionCanBeRestored(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	bodiesByID := map[string]string{"partial-a": "payload-a", "partial-b": "payload-b"}
	for index, id := range []string{"partial-a", "partial-b"} {
		if err := events.Save(context.Background(), rawBodyRetentionEvent(t, id, bodiesByID[id], time.Date(2026, 5, 2, index, 0, 0, 0, time.UTC))); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}
	makeRawBodyRetentionEligible(t, dbPath, "partial-a", "partial-b")
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	planID := "abababababababababababababababababababababababababababababababab"
	store.SetRawBodyPrunedHookForTest(func(int) error { return errors.New("injected interruption") })
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Now().UTC()); err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want interruption")
	}
	store.SetRawBodyPrunedHookForTest(nil)
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET body = 'post-plan-change', body_codec=NULL, body_format_version=NULL, body_plaintext_bytes=NULL, body_encoded_bytes=NULL, body_sha256=NULL WHERE id = 'partial-b'`); err != nil {
		t.Fatalf("update unprocessed candidate: %v", err)
	}
	_ = db.Close()
	recovery := make([]apptypes.RawBodyRecoveryBody, len(snapshot.Candidates))
	for index, candidate := range snapshot.Candidates {
		recovery[index] = apptypes.RawBodyRecoveryBody{Candidate: candidate, Body: bodiesByID[candidate.EventID]}
	}
	result, err := store.RestoreRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, recovery, time.Now().UTC())
	if err != nil {
		t.Fatalf("RestoreRawBodyPlan(partial) error = %v", err)
	}
	if result.RestoredCount != 1 || result.AlreadyRestored != 1 {
		t.Fatalf("partial restore result = %+v, want one restored and one unchanged", result)
	}
	for _, candidate := range snapshot.Candidates {
		details, err := events.GetDetails(context.Background(), types.EventID(candidate.EventID))
		if err != nil {
			t.Fatalf("GetDetails(%s) error = %v", candidate.EventID, err)
		}
		wantBody := bodiesByID[candidate.EventID]
		if candidate.EventID == "partial-b" {
			wantBody = "post-plan-change"
		}
		if !details.Event().BodyAvailability().IsAvailable() || details.Event().Body() != wantBody {
			t.Fatalf("event %s not recovered from partial execution", candidate.EventID)
		}
	}
}

func TestRawBodyRetention_restoreBetweenBatchesStopsFurtherApply(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	bodiesByID := map[string]string{"race-a": "payload-a", "race-b": "payload-b"}
	for index, id := range []string{"race-a", "race-b"} {
		if err := events.Save(context.Background(), rawBodyRetentionEvent(t, id, bodiesByID[id], time.Date(2026, 5, 3, index, 0, 0, 0, time.UTC))); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}
	makeRawBodyRetentionEligible(t, dbPath, "race-a", "race-b")
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	planID := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	recovery := make([]apptypes.RawBodyRecoveryBody, len(snapshot.Candidates))
	for index, candidate := range snapshot.Candidates {
		recovery[index] = apptypes.RawBodyRecoveryBody{Candidate: candidate, Body: bodiesByID[candidate.EventID]}
	}
	var restoreErr error
	store.SetRawBodyPrunedHookForTest(func(int) error {
		store.SetRawBodyPrunedHookForTest(nil)
		_, restoreErr = store.RestoreRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, recovery, time.Now().UTC())
		return nil
	})
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, planID, snapshot.Candidates, time.Now().UTC()); err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want restored-state stop")
	}
	if restoreErr != nil {
		t.Fatalf("RestoreRawBodyPlan() error = %v", restoreErr)
	}
	for _, candidate := range snapshot.Candidates {
		details, err := events.GetDetails(context.Background(), types.EventID(candidate.EventID))
		if err != nil {
			t.Fatalf("GetDetails(%s) error = %v", candidate.EventID, err)
		}
		if !details.Event().BodyAvailability().IsAvailable() || details.Event().Body() != bodiesByID[candidate.EventID] {
			t.Fatalf("event %s changed after restore won the batch boundary", candidate.EventID)
		}
	}
}

func TestRawBodyRetention_planIsBoundToCopiedStorePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	originalPath := filepath.Join(dir, "original.db")
	events, store := newEventDatasource(t, originalPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	event := rawBodyRetentionEvent(t, "path-bound", "payload", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	makeRawBodyRetentionEligible(t, originalPath, event.EventID().String())
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	data, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("read original DB: %v", err)
	}
	copyPath := filepath.Join(dir, "copy.db")
	if err := os.WriteFile(copyPath, data, 0o600); err != nil {
		t.Fatalf("write copied DB: %v", err)
	}
	_, copiedStore := newEventDatasource(t, copyPath, onDiskSQLiteMigrations(t))
	if err := copiedStore.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(copy) error = %v", err)
	}
	if _, err := copiedStore.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, snapshot.MigrationDigest, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", snapshot.Candidates, time.Now().UTC()); err == nil {
		t.Fatal("ApplyRawBodyPlan(copy) error = nil, want source-path mismatch")
	}
}

func rawBodyRetentionEvent(t *testing.T, id, body string, createdAt time.Time) *model.Event {
	t.Helper()
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("retention-session")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	return model.EventOf(eventID, types.EventKindTranscript, "cli", agent, sessionID, "repo", body, createdAt)
}

func makeRawBodyRetentionEligible(t *testing.T, dbPath string, eventIDs ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO sessions(session_id, started_at, ended_at, client, agent, workspace)
VALUES ('retention-session', '2026-05-01T00:00:00Z', '2026-06-02T00:00:00Z', 'cli', 'codex', 'repo')
ON CONFLICT(session_id) DO UPDATE SET ended_at = excluded.ended_at`); err != nil {
		t.Fatalf("insert ended session: %v", err)
	}
	if len(eventIDs) == 0 {
		return
	}
	if _, err := db.Exec(`INSERT INTO session_refinements(session_id, generation, covers_from_event_id, covers_to_event_id, summary, produced_by, produced_at, degraded)
VALUES (?, 1, ?, ?, 'summary', 'test', '2026-06-02T00:00:00Z', 0)
ON CONFLICT(session_id) DO UPDATE SET covers_from_event_id = excluded.covers_from_event_id, covers_to_event_id = excluded.covers_to_event_id`, "retention-session", eventIDs[0], eventIDs[len(eventIDs)-1]); err != nil {
		t.Fatalf("insert refinement: %v", err)
	}
}

func rawBodyMetadataRow(t *testing.T, dbPath, eventID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var kind, client, agent, sessionID, workspace, createdAt string
	var original, stored, ingest, storage, version sql.NullInt64
	if err := db.QueryRow(`SELECT kind, client, agent, session_id, workspace, created_at,
body_original_bytes, body_stored_bytes, body_ingest_truncated, body_storage_truncated, body_metadata_version
FROM events WHERE id = ?`, eventID).Scan(&kind, &client, &agent, &sessionID, &workspace, &createdAt, &original, &stored, &ingest, &storage, &version); err != nil {
		t.Fatalf("metadata query: %v", err)
	}
	return kind + "|" + client + "|" + agent + "|" + sessionID + "|" + workspace + "|" + createdAt + "|" + nullableInt(original) + "|" + nullableInt(stored) + "|" + nullableInt(ingest) + "|" + nullableInt(storage) + "|" + nullableInt(version)
}

func rawBodyEncodedBytes(t *testing.T, dbPath, eventID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open(encoded bytes) error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var encoded int
	if err := db.QueryRow(`SELECT body_encoded_bytes FROM events WHERE id = ?`, eventID).Scan(&encoded); err != nil {
		t.Fatalf("read encoded bytes: %v", err)
	}
	return encoded
}

func nullableInt(value sql.NullInt64) string {
	if !value.Valid {
		return "null"
	}
	return strconv.FormatInt(value.Int64, 10)
}

func TestRawBodyRetention_classifiesExclusionReasons(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	_, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`
		INSERT INTO sessions(session_id, started_at, ended_at, client, agent, workspace) VALUES
			('excl-session-ended', '2026-05-01T00:00:00Z', '2026-06-02T00:00:00Z', 'cli', 'codex', 'repo'),
			('excl-session-active', '2026-05-01T00:00:00Z', NULL, 'cli', 'codex', 'repo')
	`); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}

	validTime := "2026-06-01T00:00:00Z"

	// not_transcript: a prompt on an *active* session. Ending the session
	// must not be implied as the fix; the row stays non-discardable.
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, agent, session_id, body, created_at)
		VALUES ('excl-not-transcript', 'prompt', 'codex', 'excl-session-active', 'body', ?)
	`, validTime); err != nil {
		t.Fatalf("insert not_transcript event: %v", err)
	}

	// already_discarded is intentionally not listed per-row: discarded
	// bodies were never in the plan, and enumerating them is unbounded.
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, agent, session_id, body, body_availability, created_at)
		VALUES ('excl-already-discarded', 'transcript', 'codex', 'excl-session-ended', '', 'unavailable_retention', ?)
	`, validTime); err != nil {
		t.Fatalf("insert already_discarded event: %v", err)
	}

	// session_missing: available transcript whose session row is gone.
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, agent, session_id, body, created_at)
		VALUES ('excl-session-missing', 'transcript', 'codex', 'excl-session-absent', 'body', ?)
	`, validTime); err != nil {
		t.Fatalf("insert session_missing event: %v", err)
	}

	// session_active: transcript with an active session (ended_at IS NULL).
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, agent, session_id, body, created_at)
		VALUES ('excl-session-active', 'transcript', 'codex', 'excl-session-active', 'body', ?)
	`, validTime); err != nil {
		t.Fatalf("insert session_active event: %v", err)
	}

	// within_retention: transcript with an invalid created_at that lexically compares before the
	// cutoff (ts_norm degrades to raw string) but fails ts_valid, so the selector rejects it.
	// Uses an ended session so the CASE reaches the ts_valid check.
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, agent, session_id, body, created_at)
		VALUES ('excl-within-retention', 'transcript', 'codex', 'excl-session-ended', 'body', '0000-00-00')
	`); err != nil {
		t.Fatalf("insert within_retention event: %v", err)
	}

	// uncovered: passes all individual allowlist conditions (transcript, available, ended session,
	// valid timestamp) but has no session_refinement covering it.
	if _, err := db.Exec(`
		INSERT INTO events(id, kind, agent, session_id, body, created_at)
		VALUES ('excl-uncovered', 'transcript', 'codex', 'excl-session-ended', 'body', ?)
	`, validTime); err != nil {
		t.Fatalf("insert uncovered event: %v", err)
	}

	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	snapshot, err := store.ListRawBodyCandidates(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}

	if len(snapshot.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want none", snapshot.Candidates)
	}

	wantByID := map[string]types.RawBodyExclusionReason{
		"excl-not-transcript":   types.RawBodyExclusionReasonNotTranscript,
		"excl-session-missing":  types.RawBodyExclusionReasonSessionMissing,
		"excl-session-active":   types.RawBodyExclusionReasonSessionActive,
		"excl-within-retention": types.RawBodyExclusionReasonWithinRetention,
		"excl-uncovered":        types.RawBodyExclusionReasonUncovered,
	}

	gotByID := make(map[string]types.RawBodyExclusionReason, len(snapshot.Excluded))
	for _, excl := range snapshot.Excluded {
		gotByID[excl.EventID] = excl.Reason
	}

	if diff := cmp.Diff(wantByID, gotByID); diff != "" {
		t.Fatalf("exclusion reasons mismatch (-want +got):\n%s", diff)
	}
}
