package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
)

// seedDedupeFixture builds a store seeded with a representative mix of hook
// prompt/transcript duplicates, deliberate far-apart repeats, command audits,
// non-hook writes, and a malformed-timestamp group. It returns the store manager
// (system under test), the event datasource (for read-surface assertions), and
// the on-disk path (for raw-SQL assertions).
func seedDedupeFixture(t *testing.T) (string, *sqlite.StoreManagementDatasource, *sqlite.EventDatasource) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	eventDS, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	type row struct {
		id, kind, agent, session, workspace, body, createdAt, sourceHook, client string
	}
	rows := []row{
		// Group A: codex prompt, three near-simultaneous duplicates (within 10s,
		// beyond the 2s write guard). Canonical = a1 (earliest).
		{"evt-a1", "prompt", "codex", "s1", "w1", "hello codex", "2026-04-10T00:00:00Z", "user_prompt_submit", "hook"},
		{"evt-a2", "prompt", "codex", "s1", "w1", "hello codex", "2026-04-10T00:00:03Z", "user_prompt_submit", "hook"},
		{"evt-a3", "prompt", "codex", "s1", "w1", "hello codex\n", "2026-04-10T00:00:05Z", "user_prompt_submit", "hook"},
		// Group B: codex prompt, deliberate repeat 60s apart (default excludes; strict includes).
		{"evt-b1", "prompt", "codex", "s1", "w1", "repeat me", "2026-04-10T00:01:00Z", "user_prompt_submit", "hook"},
		{"evt-b2", "prompt", "codex", "s1", "w1", "repeat me", "2026-04-10T00:02:00Z", "user_prompt_submit", "hook"},
		// Group C: codex transcript, near-simultaneous duplicate pair.
		{"evt-c1", "transcript", "codex", "s1", "w1", "transcript body", "2026-04-10T00:00:00Z", "stop", "hook"},
		{"evt-c2", "transcript", "codex", "s1", "w1", "transcript body", "2026-04-10T00:00:01Z", "stop", "hook"},
		// Group D: claude prompt duplicates (excluded when --client codex, included when all).
		{"evt-d1", "prompt", "claude", "s2", "w1", "claude hi", "2026-04-10T00:00:00Z", "user_prompt_submit", "hook"},
		{"evt-d2", "prompt", "claude", "s2", "w1", "claude hi", "2026-04-10T00:00:02Z", "user_prompt_submit", "hook"},
		// Group E: codex prompt with a malformed created_at — must be skipped.
		// Isolated in its own workspace so the malformed row cannot break a
		// workspace-scoped read-surface assertion (a malformed created_at would
		// fail row restoration regardless of dedupe).
		{"evt-e1", "prompt", "codex", "sbad", "wbad", "bad ts", "not-a-timestamp", "user_prompt_submit", "hook"},
		{"evt-e2", "prompt", "codex", "sbad", "wbad", "bad ts", "2026-04-10T00:00:02Z", "user_prompt_submit", "hook"},
		// Group F: command_executed hook duplicates — never eligible (command audits untouched).
		{"evt-f1", "command_executed", "codex", "s1", "w1", "ls -la", "2026-04-10T00:00:00Z", "pre_tool_use", "hook"},
		{"evt-f2", "command_executed", "codex", "s1", "w1", "ls -la", "2026-04-10T00:00:01Z", "pre_tool_use", "hook"},
		// Group G: non-hook (cli) prompt duplicates — never eligible (client filter).
		{"evt-g1", "prompt", "codex", "s1", "w1", "cli prompt", "2026-04-10T00:00:00Z", "", "cli"},
		{"evt-g2", "prompt", "codex", "s1", "w1", "cli prompt", "2026-04-10T00:00:01Z", "", "cli"},
	}
	for _, r := range rows {
		var sourceHook any
		if r.sourceHook != "" {
			sourceHook = r.sourceHook
		}
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.kind, r.agent, r.session, r.workspace, r.body, r.createdAt, sourceHook, r.client,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}

	return dbPath, storeManager, eventDS
}

func dedupeArchiveCount(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event_content_dedupe_archive`).Scan(&count); err != nil {
		t.Fatalf("archive count query error = %v", err)
	}
	return count
}

func eventExists(t *testing.T, dbPath, id string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	var one int
	switch err := db.QueryRow(`SELECT 1 FROM events WHERE id = ?`, id).Scan(&one); err {
	case nil:
		return true
	case sql.ErrNoRows:
		return false
	default:
		t.Fatalf("event exists query error = %v", err)
		return false
	}
}

func groupByKept(result apptypes.ContentEventDedupeResult) map[string][]string {
	out := map[string][]string{}
	for _, group := range result.Groups {
		dups := append([]string(nil), group.DuplicateEventIDs...)
		sort.Strings(dups)
		out[group.KeptEventID] = dups
	}
	return out
}

func TestStoreManagementDatasource_DedupeContentEvents_DryRun(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: "codex"})
	if err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}

	if result.Applied {
		t.Fatalf("Applied = true, want false for dry-run")
	}
	got := groupByKept(result)
	want := map[string][]string{
		"evt-a1": {"evt-a2", "evt-a3"},
		"evt-c1": {"evt-c2"},
	}
	if len(got) != len(want) {
		t.Fatalf("group count = %d (%v), want %d", len(got), got, len(want))
	}
	for kept, dups := range want {
		gotDups := got[kept]
		if len(gotDups) != len(dups) {
			t.Fatalf("kept %s duplicates = %v, want %v", kept, gotDups, dups)
		}
		for i := range dups {
			if gotDups[i] != dups[i] {
				t.Fatalf("kept %s duplicates = %v, want %v", kept, gotDups, dups)
			}
		}
	}
	if result.MovedCount() != 3 {
		t.Fatalf("MovedCount = %d, want 3", result.MovedCount())
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reason == "" {
		t.Fatalf("Skipped = %#v, want one malformed-timestamp skip", result.Skipped)
	}
	if len(result.Sources) != 2 {
		t.Fatalf("Sources = %#v, want prompt and transcript", result.Sources)
	}
	if result.Sources[0].Agent != "codex" || result.Sources[0].SourceHook != "stop" || result.Sources[0].CandidateCount != 1 || result.Sources[0].ScannedCount != 2 {
		t.Fatalf("transcript source = %#v", result.Sources[0])
	}
	if result.Sources[1].SourceHook != "user_prompt_submit" || result.Sources[1].CandidateCount != 2 || result.Sources[1].ScannedCount != 7 {
		t.Fatalf("prompt source = %#v", result.Sources[1])
	}

	// Dry-run must not mutate.
	if dedupeArchiveCount(t, dbPath) != 0 {
		t.Fatalf("archive count = %d, want 0 after dry-run", dedupeArchiveCount(t, dbPath))
	}
	for _, id := range []string{"evt-a2", "evt-a3", "evt-c2"} {
		if !eventExists(t, dbPath, id) {
			t.Fatalf("event %s removed during dry-run", id)
		}
	}
}

func TestStoreManagementDatasource_DedupeContentEvents_BoundsBodyScan(t *testing.T) {
	t.Parallel()
	_, storeManager, _ := seedDedupeFixture(t)

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: "codex", MaxScanRows: 3})
	if err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}
	if result.TotalEligibleCount != 9 || result.ScannedCount != 3 {
		t.Fatalf("eligible/scanned = %d/%d, want 9/3", result.TotalEligibleCount, result.ScannedCount)
	}
}

func TestStoreManagementDatasource_DedupeContentEvents_ApplyAndIdempotent(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, eventDS := seedDedupeFixture(t)
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-1", Now: now,
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	if !result.Applied || result.MovedCount() != 3 {
		t.Fatalf("apply result: applied=%v moved=%d, want true/3", result.Applied, result.MovedCount())
	}

	for _, id := range []string{"evt-a2", "evt-a3", "evt-c2"} {
		if eventExists(t, dbPath, id) {
			t.Fatalf("duplicate %s still present after apply", id)
		}
	}
	for _, id := range []string{"evt-a1", "evt-c1", "evt-b1", "evt-b2", "evt-f1", "evt-f2", "evt-g1", "evt-g2"} {
		if !eventExists(t, dbPath, id) {
			t.Fatalf("non-duplicate %s wrongly removed after apply", id)
		}
	}
	if dedupeArchiveCount(t, dbPath) != 3 {
		t.Fatalf("archive count = %d, want 3 after apply", dedupeArchiveCount(t, dbPath))
	}

	// Read-surface exclusion: quarantined rows must not come back from ListRecent.
	// Scope to workspace w1 so the deliberately malformed-timestamp fixture rows
	// (isolated in workspace wbad) cannot fail row restoration here.
	listed, err := eventDS.ListRecent(context.Background(), 100, 0,
		types.EventKind(""), types.Client(""), types.Agent(""), types.SessionID(""), types.Workspace("w1"),
		false, time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	for _, event := range listed {
		switch event.EventID().String() {
		case "evt-a2", "evt-a3", "evt-c2":
			t.Fatalf("quarantined event %s still visible in ListRecent", event.EventID().String())
		}
	}

	// Idempotency: a second apply finds nothing to move and adds no archive rows.
	second, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-2", Now: now,
	})
	if err != nil {
		t.Fatalf("second DedupeContentEvents(apply) error = %v", err)
	}
	if second.MovedCount() != 0 {
		t.Fatalf("second apply moved %d rows, want 0 (idempotent)", second.MovedCount())
	}
	if dedupeArchiveCount(t, dbPath) != 3 {
		t.Fatalf("archive count = %d after second apply, want 3", dedupeArchiveCount(t, dbPath))
	}
}

func TestStoreManagementDatasource_RestoreContentEventDedupeRun(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	if _, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-1", Now: now,
	}); err != nil {
		t.Fatalf("apply error = %v", err)
	}

	restore, err := storeManager.RestoreContentEventDedupeRun(context.Background(), "dedupe-run-1")
	if err != nil {
		t.Fatalf("RestoreContentEventDedupeRun() error = %v", err)
	}
	if restore.RestoredCount != 3 {
		t.Fatalf("RestoredCount = %d, want 3", restore.RestoredCount)
	}
	for _, id := range []string{"evt-a2", "evt-a3", "evt-c2"} {
		if !eventExists(t, dbPath, id) {
			t.Fatalf("event %s not restored", id)
		}
	}
	if dedupeArchiveCount(t, dbPath) != 0 {
		t.Fatalf("archive count = %d after restore, want 0", dedupeArchiveCount(t, dbPath))
	}

	// Restoring an unknown / already-restored run fails rather than silently succeeding.
	if _, err := storeManager.RestoreContentEventDedupeRun(context.Background(), "dedupe-run-1"); err == nil {
		t.Fatalf("expected error restoring an empty run")
	}
}

func TestStoreManagementDatasource_RestoreRefusesToOverwrite(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	if _, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-1", Now: now,
	}); err != nil {
		t.Fatalf("apply error = %v", err)
	}

	// Re-create one quarantined id directly in events to simulate a conflicting row.
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
		 VALUES ('evt-a2', 'prompt', 'codex', 's1', 'w1', 'conflict', '2026-04-10T00:00:03Z', 'user_prompt_submit', 'hook')`,
	); err != nil {
		_ = db.Close()
		t.Fatalf("insert conflicting row error = %v", err)
	}
	_ = db.Close()

	if _, err := storeManager.RestoreContentEventDedupeRun(context.Background(), "dedupe-run-1"); err == nil {
		t.Fatalf("expected restore to fail on existing event id")
	}

	// Restore was all-or-nothing: the archive still holds all three rows.
	if dedupeArchiveCount(t, dbPath) != 3 {
		t.Fatalf("archive count = %d after failed restore, want 3 (rollback)", dedupeArchiveCount(t, dbPath))
	}
	// The two non-conflicting rows must not have been restored.
	for _, id := range []string{"evt-a3", "evt-c2"} {
		if eventExists(t, dbPath, id) {
			t.Fatalf("event %s restored despite failed all-or-nothing restore", id)
		}
	}
}

// TestStoreManagementDatasource_DedupeContentEvents_UnsortedInput proves the
// planner does not depend on the store returning rows in time order.
// loadDedupeCandidates issues no ORDER BY, so SQL row order is unspecified; the
// planner sorts in Go before proximity clustering. Here the duplicate group is
// inserted in reverse-time order (latest created_at first, so the default rowid
// scan yields it first), yet the earliest created_at must still be kept and the
// near-simultaneous duplicates must still be detected as one cluster.
func TestStoreManagementDatasource_DedupeContentEvents_UnsortedInput(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	_, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	// Inserted latest → earliest so the natural (rowid) scan order is the reverse
	// of time order. If the planner trusted load order, evt-u3 (latest) would be
	// kept; the correct kept row is evt-u1 (earliest).
	rows := []struct {
		id, body, createdAt string
	}{
		{"evt-u3", "unsorted body", "2026-04-10T00:00:06Z"},
		{"evt-u1", "unsorted body", "2026-04-10T00:00:00Z"},
		{"evt-u2", "unsorted body\n", "2026-04-10T00:00:03Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, 'prompt', 'codex', 's1', 'w1', ?, ?, 'user_prompt_submit', 'hook')`,
			r.id, r.body, r.createdAt,
		); err != nil {
			_ = db.Close()
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	_ = db.Close()

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: "codex"})
	if err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}

	got := groupByKept(result)
	if len(got) != 1 {
		t.Fatalf("group count = %d (%v), want 1", len(got), got)
	}
	// Earliest parsed created_at kept despite reverse-time load order.
	dups, ok := got["evt-u1"]
	if !ok {
		t.Fatalf("kept row = %v, want evt-u1 (earliest created_at)", got)
	}
	// All three near-simultaneous rows (max gap 6s ≤ 10s window) form one cluster,
	// so both later rows are duplicates of the earliest.
	want := []string{"evt-u2", "evt-u3"}
	if len(dups) != len(want) {
		t.Fatalf("duplicates = %v, want %v", dups, want)
	}
	for i := range want {
		if dups[i] != want[i] {
			t.Fatalf("duplicates = %v, want %v", dups, want)
		}
	}
}

func TestStoreManagementDatasource_DedupeContentEvents_StrictAndAgentScope(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	t.Run("strict surfaces deliberate far-apart repeats", func(t *testing.T) {
		result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
			Agent: "codex", Strict: true,
		})
		if err != nil {
			t.Fatalf("DedupeContentEvents(strict) error = %v", err)
		}
		got := groupByKept(result)
		if _, ok := got["evt-b1"]; !ok {
			t.Fatalf("strict mode missing far-apart group b1: %v", got)
		}
	})

	t.Run("agent=all includes other agents", func(t *testing.T) {
		result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: ""})
		if err != nil {
			t.Fatalf("DedupeContentEvents(all) error = %v", err)
		}
		got := groupByKept(result)
		if _, ok := got["evt-d1"]; !ok {
			t.Fatalf("agent=all missing claude group d1: %v", got)
		}
	})

	t.Run("command audits and non-hook writes never participate", func(t *testing.T) {
		result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
			Apply: true, RunID: "dedupe-run-x", Now: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("DedupeContentEvents(all,apply) error = %v", err)
		}
		for _, group := range result.Groups {
			if group.Kind == "command_executed" {
				t.Fatalf("command_executed group selected: %#v", group)
			}
		}
		for _, id := range []string{"evt-f1", "evt-f2", "evt-g1", "evt-g2"} {
			if !eventExists(t, dbPath, id) {
				t.Fatalf("excluded event %s wrongly removed", id)
			}
		}
	})
}
