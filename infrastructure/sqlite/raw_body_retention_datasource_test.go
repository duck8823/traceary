package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

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
	beforeMetadata := rawBodyMetadataRow(t, dbPath, event.EventID().String())

	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	if len(snapshot.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(snapshot.Candidates))
	}
	planID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, planID, snapshot.Candidates, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
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

	retry, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, planID, snapshot.Candidates, time.Date(2026, 7, 2, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("retry ApplyRawBodyPlan() error = %v", err)
	}
	if retry.PrunedCount != 0 || retry.AlreadyPruned != 1 {
		t.Fatalf("retry result = %+v", retry)
	}

	recovery := []apptypes.RawBodyRecoveryBody{{Candidate: snapshot.Candidates[0], Body: event.Body()}}
	restored, err := store.RestoreRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, planID, recovery, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
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
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, planID, snapshot.Candidates, time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("ApplyRawBodyPlan() after restore error = nil, want terminal-plan rejection")
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
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET body = 'changed' WHERE id = 'stale-event'`); err != nil {
		t.Fatalf("mutate body: %v", err)
	}
	_ = db.Close()

	_, err = store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", snapshot.Candidates, time.Now().UTC())
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
	if len(snapshot.Candidates) != 0 || len(snapshot.ExcludedActive) != 1 || snapshot.ExcludedActive[0] != "active-event" {
		t.Fatalf("snapshot candidates=%+v exclusions=%+v", snapshot.Candidates, snapshot.ExcludedActive)
	}
}

func TestRawBodyRetention_interruptionRollsBackAndRetryPrunesExactCandidates(t *testing.T) {
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
	snapshot, err := store.ListRawBodyCandidates(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListRawBodyCandidates() error = %v", err)
	}
	planID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	store.SetRawBodyPrunedHookForTest(func(int) error { return errors.New("injected interruption") })
	if _, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, planID, snapshot.Candidates, time.Now().UTC()); err == nil {
		t.Fatal("ApplyRawBodyPlan() error = nil, want interruption")
	}
	for _, candidate := range snapshot.Candidates {
		details, err := events.GetDetails(context.Background(), types.EventID(candidate.EventID))
		if err != nil {
			t.Fatalf("GetDetails(%s) error = %v", candidate.EventID, err)
		}
		if !details.Event().BodyAvailability().IsAvailable() {
			t.Fatalf("event %s was pruned by rolled-back transaction", candidate.EventID)
		}
	}
	store.SetRawBodyPrunedHookForTest(nil)
	result, err := store.ApplyRawBodyPlan(context.Background(), snapshot.DatabaseIdentity, snapshot.SQLiteUserVersion, planID, snapshot.Candidates, time.Now().UTC())
	if err != nil {
		t.Fatalf("retry ApplyRawBodyPlan() error = %v", err)
	}
	if result.PrunedCount != len(snapshot.Candidates) {
		t.Fatalf("retry pruned = %d, want %d", result.PrunedCount, len(snapshot.Candidates))
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
	return model.EventOf(eventID, types.EventKindNote, "cli", agent, sessionID, "repo", body, createdAt)
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

func nullableInt(value sql.NullInt64) string {
	if !value.Valid {
		return "null"
	}
	return strconv.FormatInt(value.Int64, 10)
}
