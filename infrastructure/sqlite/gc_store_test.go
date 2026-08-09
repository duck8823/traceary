package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestDatasource_CollectGarbage_DryRun(t *testing.T) {
	t.Parallel()

	dbPath, fixture := prepareGCFixture(t)

	deletedCount, err := fixture.storeManager.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		apptypes.GarbageCollectionTargetEvents,
		true,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(0, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(2, countEvents(t, dbPath)); diff != "" {
		t.Fatalf("event count mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_CollectGarbage_DryRunSucceedsOnReadOnlyStore(t *testing.T) {
	t.Parallel()

	dbPath, fixture := prepareGCFixture(t)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("ReadFile() before dry-run error = %v", err)
	}
	beforeDigest := sha256.Sum256(before)

	if err := os.Chmod(dbPath, 0o400); err != nil {
		t.Fatalf("Chmod(read-only) error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			t.Errorf("Chmod(restore) error = %v", err)
		}
	})
	beforeInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Stat() before dry-run error = %v", err)
	}

	deletedCount, err := fixture.storeManager.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		apptypes.GarbageCollectionTargetEvents,
		true,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(0, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}

	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("ReadFile() after dry-run error = %v", err)
	}
	afterDigest := sha256.Sum256(after)
	if diff := cmp.Diff(beforeDigest, afterDigest); diff != "" {
		t.Fatalf("database digest changed during dry-run (-before +after):\n%s", diff)
	}
	afterInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Stat() after dry-run error = %v", err)
	}
	if beforeInfo.Size() != afterInfo.Size() {
		t.Fatalf("database size changed during dry-run: before=%d after=%d", beforeInfo.Size(), afterInfo.Size())
	}
	if beforeInfo.Mode() != afterInfo.Mode() {
		t.Fatalf("database mode changed during dry-run: before=%s after=%s", beforeInfo.Mode(), afterInfo.Mode())
	}
	if !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("database mtime changed during dry-run: before=%s after=%s", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

func TestDatasource_CollectGarbage_DryRunDoesNotCreateMissingStore(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "missing", "traceary.db")
	storeManager := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(dbPath, fstest.MapFS{}))

	if _, err := storeManager.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		apptypes.GarbageCollectionTargetEvents,
		true,
	); err == nil {
		t.Fatal("CollectGarbage() error = nil, want missing-store error")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("Stat() error = %v, want store to remain absent", err)
	}
}

func TestDatasource_CollectGarbage_preservesUncoveredOldEventsAndAudits(t *testing.T) {
	t.Parallel()

	dbPath, fixture := prepareGCFixture(t)

	deletedCount, err := fixture.storeManager.CollectGarbage(
		context.Background(),
		time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
		apptypes.GarbageCollectionTargetEvents,
		false,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(0, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(2, countEvents(t, dbPath)); diff != "" {
		t.Fatalf("event count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, countCommandAudits(t, dbPath)); diff != "" {
		t.Fatalf("command audit count mismatch (-want +got):\n%s", diff)
	}
}

type gcFixture struct {
	eventDS      *sqlite.EventDatasource
	storeManager *sqlite.StoreManagementDatasource
}

func prepareGCFixture(t *testing.T) (string, *gcFixture) {
	t.Helper()

	migrations := fstest.MapFS{
		"000001_init.sql": {
			Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    body_availability TEXT NOT NULL DEFAULT 'available',
    created_at TEXT NOT NULL,
    source_hook TEXT
);`),
		},
		"000002_add_event_metadata.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`),
		},
		"000003_create_command_audits.sql": {
			Data: []byte(`
CREATE TABLE command_audits (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    command_text TEXT NOT NULL,
    command_wrapper TEXT NOT NULL DEFAULT '',
    command_name TEXT NOT NULL DEFAULT 'unknown',
    input_text TEXT NOT NULL,
    output_text TEXT NOT NULL,
    input_truncated INTEGER NOT NULL DEFAULT 0,
    output_truncated INTEGER NOT NULL DEFAULT 0,
    input_original_bytes INTEGER NOT NULL DEFAULT 0,
    output_original_bytes INTEGER NOT NULL DEFAULT 0,
    exit_code INTEGER,
    failed INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT 'unknown'
);`),
		},
		"000004_add_gc_discard_dependencies.sql": {
			Data: []byte(`
ALTER TABLE events ADD COLUMN body_codec TEXT;
ALTER TABLE events ADD COLUMN body_format_version INTEGER;
ALTER TABLE events ADD COLUMN body_plaintext_bytes INTEGER;
ALTER TABLE events ADD COLUMN body_encoded_bytes INTEGER;
ALTER TABLE events ADD COLUMN body_sha256 TEXT;
ALTER TABLE events ADD COLUMN body_pruned_at TEXT;
ALTER TABLE events ADD COLUMN body_pruned_plan_id TEXT;
ALTER TABLE command_audits ADD COLUMN command_codec TEXT;
ALTER TABLE command_audits ADD COLUMN command_format_version TEXT;
ALTER TABLE command_audits ADD COLUMN command_plaintext_bytes TEXT;
ALTER TABLE command_audits ADD COLUMN command_encoded_bytes TEXT;
ALTER TABLE command_audits ADD COLUMN command_sha256 TEXT;
ALTER TABLE command_audits ADD COLUMN input_codec TEXT;
ALTER TABLE command_audits ADD COLUMN input_format_version TEXT;
ALTER TABLE command_audits ADD COLUMN input_plaintext_bytes TEXT;
ALTER TABLE command_audits ADD COLUMN input_encoded_bytes TEXT;
ALTER TABLE command_audits ADD COLUMN input_sha256 TEXT;
ALTER TABLE command_audits ADD COLUMN output_codec TEXT;
ALTER TABLE command_audits ADD COLUMN output_format_version TEXT;
ALTER TABLE command_audits ADD COLUMN output_plaintext_bytes TEXT;
ALTER TABLE command_audits ADD COLUMN output_encoded_bytes TEXT;
ALTER TABLE command_audits ADD COLUMN output_sha256 TEXT;
CREATE TABLE sessions (session_id TEXT PRIMARY KEY, ended_at TEXT);
CREATE TABLE session_refinements (session_id TEXT PRIMARY KEY, covers_from_event_id TEXT NOT NULL, covers_to_event_id TEXT NOT NULL);`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, storeManager := newEventDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldAuditEvent, oldCommandAudit := newOldAuditFixture(t)
	if err := eventDS.SaveWithAudit(context.Background(), oldAuditEvent, oldCommandAudit); err != nil {
		t.Fatalf("SaveWithAudit(old) error = %v", err)
	}
	newNoteEvent := newGCEventFixture(
		t,
		"event-new",
		types.EventKindNote,
		"recent note",
		time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
	)
	if err := eventDS.Save(context.Background(), newNoteEvent); err != nil {
		t.Fatalf("Save(new) error = %v", err)
	}

	return dbPath, &gcFixture{eventDS: eventDS, storeManager: storeManager}
}

func newOldAuditFixture(t *testing.T) (*model.Event, *model.CommandAudit) {
	t.Helper()

	event := newGCEventFixture(
		t,
		"event-old",
		types.EventKindCommandExecuted,
		"go test ./...",
		time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
	)
	commandAudit, err := model.NewCommandAudit(
		event.EventID(),
		"go test ./...",
		"stdin",
		"stdout",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("NewCommandAudit() error = %v", err)
	}

	return event, commandAudit
}

func newGCEventFixture(
	t *testing.T,
	eventIDValue string,
	kind types.EventKind,
	body string,
	createdAt time.Time,
) *model.Event {
	t.Helper()

	eventID, err := types.EventIDFrom(eventIDValue)
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}

	return model.EventOf(
		eventID,
		kind,
		"cli",
		agent,
		sessionID,
		"duck8823/traceary",
		body,
		createdAt,
	)
}

func countEvents(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("event count query error = %v", err)
	}

	return count
}

func countCommandAudits(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_audits`).Scan(&count); err != nil {
		t.Fatalf("command_audits count query error = %v", err)
	}

	return count
}

func TestDatasource_CollectGarbage_deletesOldEmptySessionsButProtectsActiveAndReferenced(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	execRetentionSQL(t, db, `INSERT INTO sessions (session_id, started_at, ended_at, client, agent, repo) VALUES
		('empty-old', '2026-04-01T00:00:00Z', '2026-04-02T00:00:00Z', 'cli', 'codex', 'repo'),
		('active-old', '2026-04-01T00:00:00Z', NULL, 'cli', 'codex', 'repo'),
		('with-event-old', '2026-04-01T00:00:00Z', '2026-04-02T00:00:00Z', 'cli', 'codex', 'repo'),
		('empty-recent', '2026-04-08T00:00:00Z', '2026-04-08T01:00:00Z', 'cli', 'codex', 'repo'),
		('referenced-parent-old', '2026-04-01T00:00:00Z', '2026-04-02T00:00:00Z', 'cli', 'codex', 'repo')`)
	execRetentionSQL(t, db, `INSERT INTO sessions (session_id, started_at, ended_at, client, agent, repo, parent_session_id) VALUES
		('active-child', '2026-04-08T00:00:00Z', NULL, 'cli', 'codex', 'repo', 'referenced-parent-old')`)
	execRetentionSQL(t, db, `INSERT INTO events (id, kind, agent, session_id, body, created_at, source_hook, client, workspace) VALUES
		('event-recent', 'note', 'codex', 'with-event-old', 'recent', '2026-04-08T00:00:00Z', NULL, 'cli', 'repo')`)

	previewCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetSessions, true)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	if diff := cmp.Diff(1, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}

	deletedCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetSessions, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(1, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "sessions", "session_id", []string{"active-child", "active-old", "empty-recent", "referenced-parent-old", "with-event-old"})
	assertNoForeignKeyViolations(t, db)
}

// A surviving event keeps its session alive. Because gc no longer deletes
// event rows, the "no surviving events" condition on the empty-session sweep
// can no longer be satisfied by gc itself, so --target all removes neither.
func TestDatasource_CollectGarbageAll_keepsTheEventRowAndSoKeepsItsSession(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	execRetentionSQL(t, db, `INSERT INTO sessions (session_id, started_at, ended_at, client, agent, repo) VALUES
		('old-only', '2026-04-01T00:00:00Z', '2026-04-02T00:00:00Z', 'cli', 'codex', 'repo')`)
	execRetentionSQL(t, db, `INSERT INTO events (id, kind, agent, session_id, body, created_at, source_hook, client, workspace) VALUES
		('event-old', 'note', 'codex', 'old-only', 'old', '2026-04-02T00:00:00Z', NULL, 'cli', 'repo')`)

	previewCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetAll, true)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	if diff := cmp.Diff(0, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "events", "id", []string{"event-old"})
	assertRetentionIDs(t, db, "sessions", "session_id", []string{"old-only"})

	deletedCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetAll, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(0, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "events", "id", []string{"event-old"})
	assertRetentionIDs(t, db, "sessions", "session_id", []string{"old-only"})
	assertNoForeignKeyViolations(t, db)
}

func TestDatasource_CollectGarbage_deletesOnlyExpiredSupersededAndRejectedMemoriesWithCascade(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	insertRetentionMemory(t, db, "mem-expired-old", "expired", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "mem-superseded-old", "superseded", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "mem-accepted-old", "accepted", "mem-superseded-old", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "mem-proposed-old", "proposed", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "mem-rejected-old", "rejected", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "mem-rejected-recent", "rejected", "", "2026-04-08T00:00:00Z")
	insertRetentionMemory(t, db, "mem-expired-recent", "expired", "", "2026-04-08T00:00:00Z")
	execRetentionSQL(t, db, `INSERT INTO memory_evidence_refs (memory_id, ordinal, ref_kind, ref_value) VALUES ('mem-expired-old', 0, 'event', 'event-1')`)
	execRetentionSQL(t, db, `INSERT INTO memory_artifact_refs (memory_id, ordinal, ref_kind, ref_value) VALUES ('mem-superseded-old', 0, 'file', 'README.md')`)
	execRetentionSQL(t, db, `INSERT INTO memory_edges (id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at) VALUES
		('edge-cascade-from', 'mem-expired-old', 'mem-accepted-old', 'related-to', '2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z'),
		('edge-cascade-to', 'mem-accepted-old', 'mem-superseded-old', 'related-to', '2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z')`)

	previewCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetMemories, true)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	if diff := cmp.Diff(3, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "memory_edges", "id", []string{"edge-cascade-from", "edge-cascade-to"})

	deletedCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetMemories, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	// expired-old, superseded-old, rejected-old (rejected-recent and accepted/proposed stay)
	if diff := cmp.Diff(3, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "memories", "id", []string{"mem-accepted-old", "mem-expired-recent", "mem-proposed-old", "mem-rejected-recent"})
	assertRetentionIDs(t, db, "memory_edges", "id", nil)
	assertRetentionIDs(t, db, "memory_evidence_refs", "memory_id", nil)
	assertRetentionIDs(t, db, "memory_artifact_refs", "memory_id", nil)
	var supersedes sql.NullString
	if err := db.QueryRow(`SELECT supersedes_memory_id FROM memories WHERE id = 'mem-accepted-old'`).Scan(&supersedes); err != nil {
		t.Fatalf("query supersedes_memory_id: %v", err)
	}
	if supersedes.Valid {
		t.Fatalf("supersedes_memory_id = %q, want NULL after deleting referenced memory", supersedes.String)
	}
	assertNoForeignKeyViolations(t, db)
}

func TestDatasource_CollectGarbage_deletesStaleExtractedCandidatesAfter14d(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	stale := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	fresh := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339Nano)

	// Stale candidates from extraction: decay to expired (rows remain).
	insertRetentionMemoryWithSource(t, db, "extracted-stale", "candidate", "extracted", "", stale)
	insertRetentionMemoryWithSource(t, db, "hidden-stale", "candidate", "extracted-hidden", "", stale)
	insertRetentionMemoryWithSource(t, db, "compact-summary-stale", "candidate", "compact-summary", "", stale)
	// Fresh extracted candidate: keep as candidate.
	insertRetentionMemoryWithSource(t, db, "extracted-fresh", "candidate", "extracted", "", fresh)
	// Manual / imported candidates do not auto-decay on this short window.
	insertRetentionMemoryWithSource(t, db, "manual-old", "candidate", "manual", "", stale)
	insertRetentionMemoryWithSource(t, db, "imported-old", "candidate", "imported", "", stale)
	// Accepted extraction is curated and survives gc unless it is
	// expired/superseded.
	insertRetentionMemoryWithSource(t, db, "extracted-accepted", "accepted", "extracted", "", stale)

	// Use a `before` cutoff far in the past so the operator-controlled
	// retention does not delete anything; only the 14-day extracted
	// auto-decay should fire (status update, not hard delete).
	before := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	previewCount, err := storeManager.CollectGarbage(
		context.Background(),
		before,
		apptypes.GarbageCollectionTargetMemories,
		true,
	)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	if diff := cmp.Diff(3, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}

	deletedCount, err := storeManager.CollectGarbage(
		context.Background(),
		before,
		apptypes.GarbageCollectionTargetMemories,
		false,
	)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(3, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "memories", "id", []string{
		"compact-summary-stale",
		"extracted-accepted",
		"extracted-fresh",
		"extracted-stale",
		"hidden-stale",
		"imported-old",
		"manual-old",
	})
	// Decayed rows must be expired, not deleted.
	for _, id := range []string{"extracted-stale", "hidden-stale", "compact-summary-stale"} {
		var status string
		if err := db.QueryRow(`SELECT status FROM memories WHERE id = ?`, id).Scan(&status); err != nil {
			t.Fatalf("status %s: %v", id, err)
		}
		if status != "expired" {
			t.Fatalf("%s status = %q, want expired", id, status)
		}
	}
	assertNoForeignKeyViolations(t, db)
}

// TestDatasource_CollectGarbage_clearsSupersedesRefBeforeDeletingStaleExtractedCandidate
// verifies that an unusual graph in which a manual memory supersedes
// an auto-extracted/compact-summary candidate does not trip a foreign-key violation
// when the candidate ages out under the 14-day rule.
func TestDatasource_CollectGarbage_clearsSupersedesRefBeforeDeletingStaleExtractedCandidate(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	stale := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)

	insertRetentionMemoryWithSource(t, db, "stale-compact", "candidate", "compact-summary", "", stale)
	// Manual memory points at the stale compact-summary candidate via
	// supersedes_memory_id. The clearing pass must NULL the link
	// before the delete fires.
	insertRetentionMemoryWithSource(t, db, "manual-pointer", "accepted", "manual", "stale-compact", stale)

	if _, err := storeManager.CollectGarbage(
		context.Background(),
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		apptypes.GarbageCollectionTargetMemories,
		false,
	); err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	// Candidate is decayed to expired (still present); supersedes ref cleared.
	assertRetentionIDs(t, db, "memories", "id", []string{"manual-pointer", "stale-compact"})
	assertNoForeignKeyViolations(t, db)
	var supersedes sql.NullString
	if err := db.QueryRow(`SELECT supersedes_memory_id FROM memories WHERE id = 'manual-pointer'`).Scan(&supersedes); err != nil {
		t.Fatalf("query supersedes_memory_id: %v", err)
	}
	if supersedes.Valid {
		t.Fatalf("supersedes_memory_id = %q, want NULL after decaying referenced extracted candidate", supersedes.String)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM memories WHERE id = 'stale-compact'`).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "expired" {
		t.Fatalf("stale-compact status = %q, want expired", status)
	}
}

func TestDatasource_CollectGarbage_deletesOldClosedMemoryEdges(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	insertRetentionMemory(t, db, "mem-a", "accepted", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "mem-b", "accepted", "", "2026-04-01T00:00:00Z")
	execRetentionSQL(t, db, `INSERT INTO memory_edges (id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at) VALUES
		('edge-old-closed', 'mem-a', 'mem-b', 'related-to', '2026-04-01T00:00:00.000000000Z', '2026-04-02T00:00:00.000000000Z', '2026-04-01T00:00:00Z'),
		('edge-recent-closed', 'mem-a', 'mem-b', 'related-to', '2026-04-01T00:00:00.000000000Z', '2026-04-08T00:00:00.000000000Z', '2026-04-01T00:00:00Z'),
		('edge-open', 'mem-a', 'mem-b', 'related-to', '2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z')`)

	previewCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetMemoryEdges, true)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	if diff := cmp.Diff(1, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}

	deletedCount, err := storeManager.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetMemoryEdges, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(1, deletedCount); diff != "" {
		t.Fatalf("deletedCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "memory_edges", "id", []string{"edge-open", "edge-recent-closed"})
	assertRetentionIDs(t, db, "memories", "id", []string{"mem-a", "mem-b"})
	assertNoForeignKeyViolations(t, db)
}

func TestDatasource_CollectGarbageAll_PreviewMatchesApplyAcrossOrderedTargets(t *testing.T) {
	t.Parallel()

	dbPath, storeManager := prepareRetentionFixture(t)
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	cutoff := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	staleCandidate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)

	execRetentionSQL(t, db, `INSERT INTO sessions (session_id, started_at, ended_at, client, agent, repo) VALUES
		('old-session', '2026-04-01T00:00:00Z', '2026-04-02T00:00:00Z', 'cli', 'codex', 'repo')`)
	execRetentionSQL(t, db, `INSERT INTO events (id, kind, agent, session_id, body, created_at, source_hook, client, workspace) VALUES
		('old-event', 'note', 'codex', 'old-session', 'old', '2026-04-02T00:00:00Z', NULL, 'cli', 'repo')`)

	insertRetentionMemory(t, db, "old-memory", "expired", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "accepted-a", "accepted", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "accepted-b", "accepted", "", "2026-04-01T00:00:00Z")
	insertRetentionMemoryWithSource(t, db, "stale-candidate", "candidate", "extracted", "", staleCandidate)
	execRetentionSQL(t, db, `INSERT INTO memory_edges (id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at) VALUES
		('cascade-edge', 'old-memory', 'accepted-a', 'related-to', '2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z'),
		('independent-old-edge', 'accepted-a', 'accepted-b', 'related-to', '2026-04-01T00:00:00.000000000Z', '2026-04-02T00:00:00.000000000Z', '2026-04-01T00:00:00Z')`)

	previewCount, err := storeManager.CollectGarbage(context.Background(), cutoff, apptypes.GarbageCollectionTargetAll, true)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	// The event is a note, so it is not discardable, and its survival keeps
	// old-session referenced. Only the memory and memory-edge targets act.
	if diff := cmp.Diff(3, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}
	assertRetentionIDs(t, db, "events", "id", []string{"old-event"})
	assertRetentionIDs(t, db, "sessions", "session_id", []string{"old-session"})
	assertRetentionIDs(t, db, "memories", "id", []string{"accepted-a", "accepted-b", "old-memory", "stale-candidate"})
	assertRetentionIDs(t, db, "memory_edges", "id", []string{"cascade-edge", "independent-old-edge"})

	deletedCount, err := storeManager.CollectGarbage(context.Background(), cutoff, apptypes.GarbageCollectionTargetAll, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(previewCount, deletedCount); diff != "" {
		t.Fatalf("preview/apply count mismatch (-preview +apply):\n%s", diff)
	}
	assertRetentionIDs(t, db, "events", "id", []string{"old-event"})
	assertRetentionIDs(t, db, "sessions", "session_id", []string{"old-session"})
	assertRetentionIDs(t, db, "memories", "id", []string{"accepted-a", "accepted-b", "stale-candidate"})
	assertRetentionIDs(t, db, "memory_edges", "id", nil)

	var status string
	if err := db.QueryRow(`SELECT status FROM memories WHERE id = 'stale-candidate'`).Scan(&status); err != nil {
		t.Fatalf("query stale-candidate status: %v", err)
	}
	if status != "expired" {
		t.Fatalf("stale-candidate status = %q, want expired", status)
	}
	assertNoForeignKeyViolations(t, db)
}

func TestDatasource_CollectGarbageAll_PreviewMatchesApplyWithProductionMigrations(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	storeManager := sqlite.NewStoreManagementDatasource(sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t)))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()

	cutoff := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	staleCandidate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)

	execRetentionSQL(t, db, `INSERT INTO sessions (session_id, started_at, ended_at, client, agent, workspace) VALUES
		('old-session', '2026-04-01T00:00:00Z', '2026-04-02T00:00:00Z', 'cli', 'codex', 'repo')`)
	execRetentionSQL(t, db, `INSERT INTO events (id, kind, agent, session_id, body, created_at, source_hook, client, workspace) VALUES
		('old-event', 'note', 'codex', 'old-session', 'old', '2026-04-02T00:00:00Z', NULL, 'cli', 'repo')`)
	insertRetentionMemory(t, db, "old-memory", "expired", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "accepted-a", "accepted", "", "2026-04-01T00:00:00Z")
	insertRetentionMemory(t, db, "accepted-b", "accepted", "", "2026-04-01T00:00:00Z")
	insertRetentionMemoryWithSource(t, db, "stale-candidate", "candidate", "extracted", "", staleCandidate)
	execRetentionSQL(t, db, `INSERT INTO memory_edges (id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at) VALUES
		('cascade-edge', 'old-memory', 'accepted-a', 'related-to', '2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z'),
		('independent-old-edge', 'accepted-a', 'accepted-b', 'related-to', '2026-04-01T00:00:00.000000000Z', '2026-04-02T00:00:00.000000000Z', '2026-04-01T00:00:00Z')`)

	previewCount, err := storeManager.CollectGarbage(context.Background(), cutoff, apptypes.GarbageCollectionTargetAll, true)
	if err != nil {
		t.Fatalf("CollectGarbage(dry-run) error = %v", err)
	}
	deletedCount, err := storeManager.CollectGarbage(context.Background(), cutoff, apptypes.GarbageCollectionTargetAll, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	// Same expectation as the hand-written fixture, on the real migration set:
	// the note survives, so its session survives, and only memories and
	// memory edges are collected.
	if diff := cmp.Diff(3, previewCount); diff != "" {
		t.Fatalf("previewCount mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(previewCount, deletedCount); diff != "" {
		t.Fatalf("preview/apply count mismatch (-preview +apply):\n%s", diff)
	}
	assertRetentionIDs(t, db, "events", "id", []string{"old-event"})
	assertRetentionIDs(t, db, "sessions", "session_id", []string{"old-session"})
	assertRetentionIDs(t, db, "memories", "id", []string{"accepted-a", "accepted-b", "stale-candidate"})
	assertRetentionIDs(t, db, "memory_edges", "id", nil)
	assertNoForeignKeyViolations(t, db)
}

func prepareRetentionFixture(t *testing.T) (string, *sqlite.StoreManagementDatasource) {
	t.Helper()

	migrations := fstest.MapFS{
		"000001_retention_schema.sql": {Data: []byte(`
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    body_availability TEXT NOT NULL DEFAULT 'available',
    created_at TEXT NOT NULL,
    source_hook TEXT,
    client TEXT NOT NULL DEFAULT '',
    workspace TEXT NOT NULL DEFAULT ''
);
CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    repo TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id)
);
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    scope_kind TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    fact TEXT NOT NULL,
    status TEXT NOT NULL,
    confidence TEXT NOT NULL,
    source TEXT NOT NULL,
    supersedes_memory_id TEXT REFERENCES memories(id),
    expires_at TEXT,
    valid_from TEXT,
    valid_to TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE memory_evidence_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);
CREATE TABLE memory_artifact_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);
CREATE TABLE memory_edges (
    id TEXT PRIMARY KEY,
    from_memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    to_memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,
    valid_from TEXT NOT NULL,
    valid_to TEXT,
    created_at TEXT NOT NULL
);`)},
	}
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	storeManager := newStoreManagementDatasource(t, dbPath, migrations)
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return dbPath, storeManager
}

func openRetentionDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return db
}

func execRetentionSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func insertRetentionMemory(t *testing.T, db *sql.DB, id, status, supersedesID, updatedAt string) {
	t.Helper()
	insertRetentionMemoryWithSource(t, db, id, status, "manual", supersedesID, updatedAt)
}

func insertRetentionMemoryWithSource(t *testing.T, db *sql.DB, id, status, source, supersedesID, updatedAt string) {
	t.Helper()
	var supersedes any
	if supersedesID != "" {
		supersedes = supersedesID
	}
	execRetentionSQL(t, db, `INSERT INTO memories (id, type, scope_kind, scope_value, fact, status, confidence, source, supersedes_memory_id, expires_at, valid_from, valid_to, created_at, updated_at)
		VALUES (?, 'preference', 'workspace', 'repo', ?, ?, 'medium', ?, ?, NULL, '2026-04-01T00:00:00.000000000Z', NULL, '2026-04-01T00:00:00Z', ?)`, id, id, status, source, supersedes, updatedAt)
}

func assertRetentionIDs(t *testing.T, db *sql.DB, table, column string, want []string) {
	t.Helper()
	rows, err := db.Query(`SELECT ` + column + ` FROM ` + table + ` ORDER BY ` + column)
	if err != nil {
		t.Fatalf("query %s.%s: %v", table, column, err)
	}
	defer func() { _ = rows.Close() }()
	got := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan %s.%s: %v", table, column, err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s.%s: %v", table, column, err)
	}
	if want == nil {
		want = []string{}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s.%s ids mismatch (-want +got):\n%s", table, column, diff)
	}
}

func assertNoForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatalf("foreign_key_check returned at least one violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check iteration: %v", err)
	}
}

func TestDatasource_CollectGarbage_DryRunSkipsUnsupportedLegacyDiscardPredicate(t *testing.T) {
	t.Parallel()

	// Only the events target is exercised here: the fixture deliberately stops
	// at the fold schema, so it cannot serve the memory targets that --target
	// all also counts. That the other targets keep counting when this branch
	// is skipped is pinned by
	// TestDatasource_CollectGarbageAll_PreviewMatchesApplyAcrossOrderedTargets.
	dbPath, store := prepareLegacyGCFixture(t)
	got, err := store.CollectGarbage(context.Background(), time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetEvents, true)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(0, got); diff != "" {
		t.Fatalf("discard count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(1, countEvents(t, dbPath)); diff != "" {
		t.Fatalf("event count mismatch (-want +got):\n%s", diff)
	}
}

func prepareLegacyGCFixture(t *testing.T) (string, *sqlite.StoreManagementDatasource) {
	t.Helper()
	// Mirrors the schema the released binary writes today: body_availability
	// and sessions are present, session_refinements is not, so there is no
	// fold evidence for the discard predicate to read.
	migrations := fstest.MapFS{"000001_init.sql": {Data: []byte(`
CREATE TABLE events (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, agent TEXT NOT NULL, session_id TEXT NOT NULL,
 body TEXT NOT NULL, body_availability TEXT NOT NULL DEFAULT 'available', created_at TEXT NOT NULL, source_hook TEXT
);
CREATE TABLE sessions (session_id TEXT PRIMARY KEY, started_at TEXT NOT NULL, ended_at TEXT);`)}}
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	store := newStoreManagementDatasource(t, dbPath, migrations)
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	execRetentionSQL(t, db, `INSERT INTO events(id, kind, agent, session_id, body, body_availability, created_at) VALUES ('legacy', 'transcript', 'codex', 'legacy-session', 'body', 'available', '2026-04-01T00:00:00Z')`)
	return dbPath, store
}

func TestDatasource_CollectGarbage_discardsOnlyFoldedEndedTranscripts(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		kind    types.EventKind
		session string
		ended   bool
		folded  bool
		target  apptypes.GarbageCollectionTarget
		want    string
	}{
		{name: "folded ended transcript", kind: types.EventKindTranscript, session: "ended", ended: true, folded: true, target: apptypes.GarbageCollectionTargetEvents, want: "unavailable_retention"},
		{name: "prompt survives events", kind: types.EventKindPrompt, session: "ended", ended: true, folded: true, target: apptypes.GarbageCollectionTargetEvents, want: "available"},
		{name: "prompt survives all", kind: types.EventKindPrompt, session: "ended", ended: true, folded: true, target: apptypes.GarbageCollectionTargetAll, want: "available"},
		{name: "prompt survives sessions", kind: types.EventKindPrompt, session: "ended", ended: true, folded: true, target: apptypes.GarbageCollectionTargetSessions, want: "available"},
		{name: "note survives", kind: types.EventKindNote, session: "ended", ended: true, folded: true, target: apptypes.GarbageCollectionTargetEvents, want: "available"},
		{name: "unfolded transcript survives", kind: types.EventKindTranscript, session: "ended", ended: true, target: apptypes.GarbageCollectionTargetEvents, want: "available"},
		{name: "missing session survives", kind: types.EventKindTranscript, session: "missing", target: apptypes.GarbageCollectionTargetEvents, want: "available"},
		{name: "active session survives", kind: types.EventKindTranscript, session: "active", folded: true, target: apptypes.GarbageCollectionTargetEvents, want: "available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, events, store := prepareDiscardGCFixture(t)
			event := newGCEventFixture(t, "event-1", tc.kind, "retained metadata", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
			if err := events.Save(context.Background(), event); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			db := openRetentionDB(t, dbPath)
			defer func() { _ = db.Close() }()
			if tc.session != "missing" {
				insertGCSession(t, db, "session-1", tc.ended)
			}
			if tc.folded {
				insertGCFold(t, db, "session-1", "event-1", "event-1")
			}
			before := gcEventPreservedFields(t, db, "event-1")
			got, err := store.CollectGarbage(context.Background(), cutoff, tc.target, false)
			if err != nil {
				t.Fatalf("CollectGarbage() error = %v", err)
			}
			wantCount := 0
			if tc.want == "unavailable_retention" {
				wantCount = 1
			}
			if diff := cmp.Diff(wantCount, got); diff != "" {
				t.Fatalf("discard count mismatch (-want +got):\n%s", diff)
			}
			availability := gcEventAvailability(t, db, "event-1")
			if diff := cmp.Diff(tc.want, availability); diff != "" {
				t.Fatalf("availability mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(before, gcEventPreservedFields(t, db, "event-1")); diff != "" {
				t.Fatalf("preserved fields changed (-before +after):\n%s", diff)
			}
		})
	}
}

func TestDatasource_CollectGarbage_preservesCommandAuditAndDecodesDiscardMarker(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	transcript := newGCEventFixture(t, "transcript", types.EventKindTranscript, "discard me", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), transcript); err != nil {
		t.Fatalf("Save(transcript) error = %v", err)
	}
	command, audit := newOldAuditFixture(t)
	if err := events.SaveWithAudit(context.Background(), command, audit); err != nil {
		t.Fatalf("SaveWithAudit() error = %v", err)
	}
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertGCSession(t, db, "session-1", true)
	insertGCFold(t, db, "session-1", "transcript", "transcript")
	prunedAt := time.Date(2026, 7, 2, 3, 4, 5, 6, time.UTC)
	store.SetGarbageCollectionNowForTest(func() time.Time { return prunedAt })
	var commandText, inputText string
	if err := db.QueryRow(`SELECT command_text, input_text FROM command_audits WHERE event_id = ?`, command.EventID().String()).Scan(&commandText, &inputText); err != nil {
		t.Fatalf("read command audit: %v", err)
	}
	got, err := store.CollectGarbage(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetAll, false)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(1, got); diff != "" {
		t.Fatalf("discard count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(2, countEvents(t, dbPath)); diff != "" {
		t.Fatalf("event row count mismatch (-want +got):\n%s", diff)
	}
	var recordedPrunedAt string
	if err := db.QueryRow(`SELECT body_pruned_at FROM events WHERE id = 'transcript'`).Scan(&recordedPrunedAt); err != nil {
		t.Fatalf("query body_pruned_at: %v", err)
	}
	if diff := cmp.Diff(prunedAt.Format(time.RFC3339Nano), recordedPrunedAt); diff != "" {
		t.Fatalf("body_pruned_at mismatch (-want +got):\n%s", diff)
	}
	var afterCommand, afterInput string
	if err := db.QueryRow(`SELECT command_text, input_text FROM command_audits WHERE event_id = ?`, command.EventID().String()).Scan(&afterCommand, &afterInput); err != nil {
		t.Fatalf("read command audit after gc: %v", err)
	}
	if diff := cmp.Diff([]string{commandText, inputText}, []string{afterCommand, afterInput}); diff != "" {
		t.Fatalf("command audit changed (-before +after):\n%s", diff)
	}
	plaintext, err := sqlite.LoadEventPlaintextForTest(context.Background(), db, transcript.EventID().String())
	if err != nil {
		t.Fatalf("loadEventPlaintext() error = %v", err)
	}
	if diff := cmp.Diff(types.EventBodyUnavailableRetentionMarker, string(plaintext)); diff != "" {
		t.Fatalf("decoded marker mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasource_CollectGarbage_DryRunReportsEligibleBodiesWithoutMutation(t *testing.T) {
	t.Parallel()
	dbPath, events, store := prepareDiscardGCFixture(t)
	event := newGCEventFixture(t, "eligible", types.EventKindTranscript, "body", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := events.Save(context.Background(), event); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	db := openRetentionDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertGCSession(t, db, "session-1", true)
	insertGCFold(t, db, "session-1", "eligible", "eligible")
	got, err := store.CollectGarbage(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), apptypes.GarbageCollectionTargetEvents, true)
	if err != nil {
		t.Fatalf("CollectGarbage() error = %v", err)
	}
	if diff := cmp.Diff(1, got); diff != "" {
		t.Fatalf("candidate count mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("available", gcEventAvailability(t, db, "eligible")); diff != "" {
		t.Fatalf("availability mismatch (-want +got):\n%s", diff)
	}
}

func prepareDiscardGCFixture(t *testing.T) (string, *sqlite.EventDatasource, *sqlite.StoreManagementDatasource) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	events, store := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return dbPath, events, store
}
func insertGCSession(t *testing.T, db *sql.DB, id string, ended bool) {
	t.Helper()
	var endedAt any
	if ended {
		endedAt = "2026-06-02T00:00:00Z"
	}
	execRetentionSQL(t, db, `INSERT INTO sessions(session_id, started_at, ended_at, client, agent, workspace) VALUES (?, '2026-06-01T00:00:00Z', ?, 'cli', 'codex', 'repo')`, id, endedAt)
}
func insertGCFold(t *testing.T, db *sql.DB, sessionID, from, to string) {
	t.Helper()
	execRetentionSQL(t, db, `INSERT INTO session_refinements(session_id, generation, covers_from_event_id, covers_to_event_id, summary, produced_by, produced_at, degraded) VALUES (?, 1, ?, ?, 'summary', 'test', '2026-06-02T00:00:00Z', 0)`, sessionID, from, to)
}
func gcEventAvailability(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(`SELECT body_availability FROM events WHERE id = ?`, id).Scan(&value); err != nil {
		t.Fatalf("query availability: %v", err)
	}
	return value
}
func gcEventPreservedFields(t *testing.T, db *sql.DB, id string) []string {
	t.Helper()
	var original, stored sql.NullInt64
	var createdAt, kind string
	var count int
	if err := db.QueryRow(`SELECT body_original_bytes, body_stored_bytes, created_at, kind FROM events WHERE id = ?`, id).Scan(&original, &stored, &createdAt, &kind); err != nil {
		t.Fatalf("query preserved fields: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return []string{nullableInt(original), nullableInt(stored), createdAt, kind, strconv.Itoa(count)}
}
