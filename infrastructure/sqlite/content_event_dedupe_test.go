package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	_ "modernc.org/sqlite"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
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

// seedUnreadableBodyDedupeFixture builds a store with one near-simultaneous
// decodable duplicate pair, one row whose body cannot be decoded, and a second
// decodable duplicate pair under a different hook. It is deliberately separate
// from seedDedupeFixture, whose exact ScannedCount/Sources assertions a new row
// would break.
func seedUnreadableBodyDedupeFixture(t *testing.T) (string, *sqlite.StoreManagementDatasource) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	_, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	type row struct {
		id, kind, body, createdAt, sourceHook string
	}
	// Pair 1: near-simultaneous prompt duplicates. Canonical = u1 (earliest).
	for _, r := range []row{
		{"evt-u1", "prompt", "hello codex", "2026-05-01T00:00:00Z", "user_prompt_submit"},
		{"evt-u2", "prompt", "hello codex", "2026-05-01T00:00:03Z", "user_prompt_submit"},
	} {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, ?, 'codex', 's1', 'w1', ?, ?, ?, 'hook')`,
			r.id, r.kind, r.body, r.createdAt, r.sourceHook,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}

	// evt-x1: a row whose body cannot be decoded. body_codec is set but the
	// other four payload metadata columns are left NULL, so payloadRow.decode
	// reports "incomplete metadata" -- a tolerable *PayloadIntegrityError -- at
	// negligible fixture cost (no oversized blob needed). Same (agent,
	// source_hook) bucket as pair 1, inserted between the two pairs, so it also
	// exercises B4 (both surrounding groups still identified) and B6 (scanned
	// count includes it, candidate count does not).
	if _, err := db.Exec(
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_codec)
		 VALUES ('evt-x1', 'prompt', 'codex', 's1', 'w1', 'corrupt', '2026-05-01T00:00:05Z', 'user_prompt_submit', 'hook', 'zstd')`,
	); err != nil {
		t.Fatalf("insert evt-x1 error = %v", err)
	}

	// Pair 2 under a different hook.
	for _, r := range []row{
		{"evt-v1", "transcript", "transcript body", "2026-05-01T00:01:00Z", "stop"},
		{"evt-v2", "transcript", "transcript body", "2026-05-01T00:01:01Z", "stop"},
	} {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, ?, 'codex', 's1', 'w1', ?, ?, ?, 'hook')`,
			r.id, r.kind, r.body, r.createdAt, r.sourceHook,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}

	var availability string
	if err := db.QueryRow(`SELECT body_availability FROM events WHERE id = 'evt-x1'`).Scan(&availability); err != nil {
		t.Fatalf("read evt-x1 body_availability error = %v", err)
	}
	if availability != "available" {
		t.Fatalf("evt-x1 body_availability = %q, want available (so it stays eligible)", availability)
	}

	return dbPath, storeManager
}

// B1/B2/B4/B6: one unreadable body between two decodable duplicate groups
// does not stop identification, the remaining groups are still planned, the
// unreadable row is reported as its own distinguishable skip entry, and the
// counts stay honest.
func TestStoreManagementDatasource_DedupeContentEvents_UnreadableBody_DryRun(t *testing.T) {
	t.Parallel()
	dbPath, storeManager := seedUnreadableBodyDedupeFixture(t)

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: "codex"})
	if err != nil {
		t.Fatalf("DedupeContentEvents() error = %v", err)
	}

	// B1 + B4: both surrounding groups are still planned.
	if diff := cmp.Diff(map[string][]string{
		"evt-u1": {"evt-u2"},
		"evt-v1": {"evt-v2"},
	}, groupByKept(result)); diff != "" {
		t.Fatalf("plan (-want +got):\n%s", diff)
	}
	if result.MovedCount() != 2 {
		t.Fatalf("MovedCount = %d, want 2", result.MovedCount())
	}

	// B2: the unreadable row is reported distinguishably, not silently dropped.
	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %#v, want exactly one entry", result.Skipped)
	}
	skip := result.Skipped[0]
	if diff := cmp.Diff([]string{"evt-x1"}, skip.EventIDs); diff != "" {
		t.Fatalf("Skipped[0].EventIDs (-want +got):\n%s", diff)
	}
	if !strings.HasPrefix(skip.Reason, "skipped: unreadable body") {
		t.Fatalf("Skipped[0].Reason = %q, want the unreadable-body prefix", skip.Reason)
	}
	if skip.Reason == "skipped: malformed or unparseable created_at" {
		t.Fatalf("Skipped[0].Reason = %q, must not equal the malformed-timestamp constant", skip.Reason)
	}
	if !strings.HasSuffix(skip.GroupKey, "body:unreadable") {
		t.Fatalf("Skipped[0].GroupKey = %q, want suffix body:unreadable", skip.GroupKey)
	}

	// B6: the unreadable row still counts as scanned, in both the overall total
	// and its (agent, source_hook) bucket, but never as a candidate.
	if result.ScannedCount != 5 {
		t.Fatalf("ScannedCount = %d, want 5 (2 + 1 unreadable + 2)", result.ScannedCount)
	}
	var promptSource apptypes.ContentEventDedupeSourceStat
	found := false
	for _, source := range result.Sources {
		if source.SourceHook == "user_prompt_submit" {
			promptSource, found = source, true
		}
	}
	if !found {
		t.Fatalf("Sources = %#v, missing user_prompt_submit bucket", result.Sources)
	}
	if promptSource.ScannedCount != 3 {
		t.Fatalf("prompt bucket ScannedCount = %d, want 3 (2 decodable + 1 unreadable)", promptSource.ScannedCount)
	}
	if promptSource.CandidateCount != 1 {
		t.Fatalf("prompt bucket CandidateCount = %d, want 1 (the unreadable row is never a candidate)", promptSource.CandidateCount)
	}

	// Dry-run must not mutate.
	if dedupeArchiveCount(t, dbPath) != 0 {
		t.Fatalf("archive count = %d, want 0 after dry-run", dedupeArchiveCount(t, dbPath))
	}
}

// B3: the unreadable row is never archived, on top of the decodable pairs
// still being applied normally.
func TestStoreManagementDatasource_DedupeContentEvents_UnreadableBody_Apply(t *testing.T) {
	t.Parallel()
	dbPath, storeManager := seedUnreadableBodyDedupeFixture(t)
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-unreadable-1", Now: now,
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	if result.MovedCount() != 2 {
		t.Fatalf("MovedCount = %d, want 2", result.MovedCount())
	}
	if !eventExists(t, dbPath, "evt-x1") {
		t.Fatal("unreadable row evt-x1 was removed by apply")
	}
	for _, id := range []string{"evt-u2", "evt-v2"} {
		if eventExists(t, dbPath, id) {
			t.Fatalf("decodable duplicate %s was not archived", id)
		}
	}
	for _, id := range []string{"evt-u1", "evt-v1"} {
		if !eventExists(t, dbPath, id) {
			t.Fatalf("canonical row %s was wrongly removed", id)
		}
	}
	if got := dedupeArchiveCount(t, dbPath); got != 2 {
		t.Fatalf("archive count = %d, want 2 (the unreadable row is never archived)", got)
	}
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

func TestStoreManagementDatasource_DedupeContentEvents_RepointsRefinementCoverageEndpoints(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(
		`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
		 VALUES ('evt-later', 'transcript', 'codex', 's1', 'w1', 'later unique turn', '2026-04-10T00:00:30Z', 'stop', 'hook')`,
	); err != nil {
		t.Fatalf("insert later unique transcript: %v", err)
	}

	database := sqlite.NewDatabase(dbPath, onDiskSQLiteMigrations(t))
	refinementDS := sqlite.NewSessionRefinementDatasource(database)
	seed, err := model.NewSessionRefinement(
		types.SessionID("s1"), 1, types.EventID("evt-c1"), types.EventID("evt-c2"),
		"covers the later duplicate", "", "agent", now, false,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement() error = %v", err)
	}
	written, err := refinementDS.SaveIfAdvances(ctx, seed, 0)
	if err != nil || !written {
		t.Fatalf("seed SaveIfAdvances() written=%v err=%v", written, err)
	}

	if _, err := storeManager.DedupeContentEvents(ctx, apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-repoint-1", Now: now,
	}); err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	if eventExists(t, dbPath, "evt-c2") {
		t.Fatal("duplicate coverage endpoint evt-c2 still present after apply")
	}

	got, err := refinementDS.FindBySessionID(ctx, types.SessionID("s1"))
	if err != nil {
		t.Fatalf("FindBySessionID() error = %v", err)
	}
	stored, ok := got.Value()
	if !ok {
		t.Fatal("refinement missing after dedupe")
	}
	if stored.CoversToEventID() != types.EventID("evt-c1") {
		t.Fatalf("covers_to after dedupe = %q, want kept twin evt-c1", stored.CoversToEventID())
	}

	next, err := model.NewSessionRefinement(
		types.SessionID("s1"), 2, types.EventID("evt-c1"), types.EventID("evt-later"),
		"re-refined after endpoint delete", "", "agent", now.Add(time.Minute), false,
	)
	if err != nil {
		t.Fatalf("NewSessionRefinement(next) error = %v", err)
	}
	advanced, err := refinementDS.SaveIfAdvances(ctx, next, 1)
	if err != nil {
		t.Fatalf("SaveIfAdvances after dedupe error = %v", err)
	}
	if !advanced {
		t.Fatal("SaveIfAdvances after dedupe = false, want true so the session can be re-refined")
	}
	got, err = refinementDS.FindBySessionID(ctx, types.SessionID("s1"))
	if err != nil {
		t.Fatalf("FindBySessionID() after re-refine error = %v", err)
	}
	stored, ok = got.Value()
	if !ok {
		t.Fatal("refinement missing after re-refine")
	}
	if stored.Generation() != 2 || stored.CoversToEventID() != types.EventID("evt-later") {
		t.Fatalf("after re-refine gen=%d to=%q, want 2 / evt-later", stored.Generation(), stored.CoversToEventID())
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

// remainingEventIDs returns every surviving event id, sorted, so two runs can be
// compared as whole states rather than row by row.
func remainingEventIDs(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id FROM events ORDER BY id`)
	if err != nil {
		t.Fatalf("remaining events query error = %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan remaining event error = %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate remaining events error = %v", err)
	}
	return ids
}

// A repair that spans hundreds of thousands of rows cannot run as one
// transaction, so apply commits in batches. Batching is a durability and memory
// decision only: it must not change which rows survive.
func TestStoreManagementDatasource_DedupeContentEvents_BatchSizeDoesNotChangeOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		batchSize int
	}{
		{name: "one row per transaction", batchSize: 1},
		{name: "two rows per transaction", batchSize: 2},
		{name: "more rows than the plan holds", batchSize: 1000},
		{name: "zero selects the default", batchSize: 0},
	}

	var want []string
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dbPath, storeManager, _ := seedDedupeFixture(t)
			result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
				Agent: "codex", Apply: true, RunID: "dedupe-run-batch", Now: now, BatchSize: test.batchSize,
			})
			if err != nil {
				t.Fatalf("DedupeContentEvents(apply) error = %v", err)
			}
			if result.MovedCount() != 3 {
				t.Fatalf("moved = %d, want 3", result.MovedCount())
			}
			if got := dedupeArchiveCount(t, dbPath); got != 3 {
				t.Fatalf("archive count = %d, want 3", got)
			}
			got := remainingEventIDs(t, dbPath)
			if want == nil {
				want = got
				return
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("surviving events differ from the reference batch size (-want +got):\n%s", diff)
			}
		})
	}
}

// An interrupted apply leaves committed batches in place. Re-running must finish
// the repair and land on the same state a clean run would have produced, without
// failing on the rows the interrupted run already archived.
//
// The fixture archives a cluster's duplicates in full (group C: evt-c2), because
// that is the only shape an interruption can actually leave behind — apply never
// commits part of a cluster. A half-archived cluster is a different and unsafe
// state, and what keeps it unreachable is pinned by TestPartitionDedupeTargets
// rather than reproduced here.
func TestStoreManagementDatasource_DedupeContentEvents_ResumesAfterPartialApply(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	cleanPath, cleanManager, _ := seedDedupeFixture(t)
	if _, err := cleanManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-clean", Now: now,
	}); err != nil {
		t.Fatalf("clean DedupeContentEvents(apply) error = %v", err)
	}
	want := remainingEventIDs(t, cleanPath)

	// Simulate an apply that committed the batch holding group C and then died:
	// evt-c2 is archived and gone from events, group A is untouched.
	partialPath, partialManager, _ := seedDedupeFixture(t)
	archiveOneDuplicate(t, partialPath, "evt-c2", "evt-c1", "dedupe-interrupted")

	result, err := partialManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-resume", Now: now, BatchSize: 1,
	})
	if err != nil {
		t.Fatalf("resumed DedupeContentEvents(apply) error = %v", err)
	}
	if result.MovedCount() != 2 {
		t.Fatalf("resumed apply moved = %d, want 2 (group C was already archived)", result.MovedCount())
	}
	if diff := cmp.Diff(want, remainingEventIDs(t, partialPath)); diff != "" {
		t.Errorf("resumed run did not converge on the clean-run state (-want +got):\n%s", diff)
	}
}

// archiveOneDuplicate moves a single row out of events into the quarantine
// archive by hand, standing in for a batch an interrupted run had committed.
func archiveOneDuplicate(t *testing.T, dbPath, eventID, keptID, runID string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(
		`INSERT INTO event_content_dedupe_archive
		    (id, kind, client, agent, session_id, workspace, body, created_at,
		     source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
		 SELECT id, kind, client, agent, session_id, workspace, body, created_at,
		        source_hook, ?, ?, '2026-06-20T00:00:00Z', 'group', 'interrupted'
		   FROM events WHERE id = ?`,
		keptID, runID, eventID,
	); err != nil {
		t.Fatalf("hand-archive %s error = %v", eventID, err)
	}
	if _, err := db.Exec(`DELETE FROM events WHERE id = ?`, eventID); err != nil {
		t.Fatalf("hand-delete %s error = %v", eventID, err)
	}
}

// Quarantine relocates duplicates; only purge reclaims them. Purge must end the
// rollback window, so restoring a purged run has to fail rather than silently
// restore nothing.
func TestStoreManagementDatasource_PurgeContentEventDedupeRun(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	if _, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-1", Now: now,
	}); err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	before := remainingEventIDs(t, dbPath)

	result, err := storeManager.PurgeContentEventDedupeRun(context.Background(), "dedupe-run-1")
	if err != nil {
		t.Fatalf("PurgeContentEventDedupeRun() error = %v", err)
	}
	if result.PurgedCount != 3 {
		t.Errorf("purged count = %d, want 3", result.PurgedCount)
	}
	if result.ReleasedBody <= 0 {
		t.Errorf("released body bytes = %d, want a positive byte count", result.ReleasedBody)
	}
	if got := dedupeArchiveCount(t, dbPath); got != 0 {
		t.Errorf("archive count = %d after purge, want 0", got)
	}
	if diff := cmp.Diff(before, remainingEventIDs(t, dbPath)); diff != "" {
		t.Errorf("purge changed the surviving events (-want +got):\n%s", diff)
	}
	if _, err := storeManager.RestoreContentEventDedupeRun(context.Background(), "dedupe-run-1"); err == nil {
		t.Error("RestoreContentEventDedupeRun() after purge = nil error, want failure: the rollback window is over")
	}
}

func TestStoreManagementDatasource_PurgeContentEventDedupeRun_Rejects(t *testing.T) {
	t.Parallel()
	_, storeManager, _ := seedDedupeFixture(t)

	tests := []struct {
		name  string
		runID string
	}{
		{name: "empty run id", runID: "   "},
		{name: "unknown run id", runID: "dedupe-run-never-happened"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := storeManager.PurgeContentEventDedupeRun(context.Background(), test.runID); err == nil {
				t.Errorf("PurgeContentEventDedupeRun(%q) = nil error, want failure", test.runID)
			}
		})
	}
}

// The retention pruner replaces a body with one fixed marker string for every
// row it empties, regardless of client or kind. Emptied rows must therefore be
// excluded from identity grouping: otherwise unrelated prompts and transcripts
// from different sessions all hash to the same identity and dedupe quarantines
// rows that never duplicated anything.
func TestStoreManagementDatasource_DedupeContentEvents_SkipsRetentionEmptiedBodies(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	// Two prompts that were never duplicates of each other: different original
	// bodies, same session, seconds apart. Pruning left both carrying the same
	// marker text, which is exactly what would collapse them into one identity
	// group if emptied rows were eligible.
	marker := types.EventBodyUnavailableRetentionMarker
	rows := []struct{ id, createdAt string }{
		{"evt-r1", "2026-04-10T00:00:00Z"},
		{"evt-r2", "2026-04-10T00:00:02Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client, body_availability)
			 VALUES (?, 'prompt', 'codex', 's9', 'w1', ?, ?, 'user_prompt_submit', 'hook', 'unavailable_retention')`,
			r.id, marker, r.createdAt,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{Agent: "codex"})
	if err != nil {
		t.Fatalf("DedupeContentEvents(dry-run) error = %v", err)
	}
	for _, group := range result.Groups {
		for _, id := range group.DuplicateEventIDs {
			if strings.HasPrefix(id, "evt-r") {
				t.Errorf("emptied row %s was selected as a duplicate of %s", id, group.KeptEventID)
			}
		}
	}
	if diff := cmp.Diff(map[string][]string{
		"evt-a1": {"evt-a2", "evt-a3"},
		"evt-c1": {"evt-c2"},
	}, groupByKept(result)); diff != "" {
		t.Errorf("emptied rows changed the plan (-want +got):\n%s", diff)
	}
}

// A run id is the only handle on --restore and --purge, and an apply interrupted
// after its first commit has already quarantined rows under an id nothing
// printed. Listing is what keeps those rows reachable.
func TestStoreManagementDatasource_ListContentEventDedupeRuns(t *testing.T) {
	t.Parallel()
	_, storeManager, _ := seedDedupeFixture(t)

	runs, err := storeManager.ListContentEventDedupeRuns(context.Background())
	if err != nil {
		t.Fatalf("ListContentEventDedupeRuns() on an empty archive error = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d on an empty archive, want 0", len(runs))
	}

	if _, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-early",
		Now: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	if _, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "claude", Apply: true, RunID: "dedupe-run-late",
		Now: time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}

	runs, err = storeManager.ListContentEventDedupeRuns(context.Background())
	if err != nil {
		t.Fatalf("ListContentEventDedupeRuns() error = %v", err)
	}
	gotIDs := make([]string, 0, len(runs))
	gotRows := map[string]int{}
	for _, run := range runs {
		gotIDs = append(gotIDs, run.RunID)
		gotRows[run.RunID] = run.QuarantinedRows
		if run.ArchivedAt == "" {
			t.Errorf("run %s has an empty archived_at", run.RunID)
		}
		if run.BodyBytes <= 0 {
			t.Errorf("run %s body bytes = %d, want a positive byte count", run.RunID, run.BodyBytes)
		}
	}
	if diff := cmp.Diff([]string{"dedupe-run-late", "dedupe-run-early"}, gotIDs); diff != "" {
		t.Errorf("run order is not newest-first (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]int{"dedupe-run-early": 3, "dedupe-run-late": 1}, gotRows); diff != "" {
		t.Errorf("quarantined row counts (-want +got):\n%s", diff)
	}

	if _, err := storeManager.PurgeContentEventDedupeRun(context.Background(), "dedupe-run-early"); err != nil {
		t.Fatalf("PurgeContentEventDedupeRun() error = %v", err)
	}
	runs, err = storeManager.ListContentEventDedupeRuns(context.Background())
	if err != nil {
		t.Fatalf("ListContentEventDedupeRuns() after purge error = %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "dedupe-run-late" {
		t.Errorf("runs after purge = %+v, want only dedupe-run-late", runs)
	}
}

func TestListContentEventDedupeRunsReportsOldestArchivedAt(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "traceary", "traceary.db")
	_, storeManager := newEventDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := storeManager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	insertListArchiveRow(t, db, "op-run", "a-early", "2026-05-30T00:00:00Z")
	insertListArchiveRow(t, db, "op-run", "a-late", "2026-05-30T00:00:00.5Z")
	insertListArchiveRow(t, db, "compact-copy-filter-abcd", "i-1", "2026-05-30T00:00:00Z")
	runs, err := storeManager.ListContentEventDedupeRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]apptypes.ContentEventDedupeRun{}
	for _, run := range runs {
		byID[run.RunID] = run
	}
	op := byID["op-run"]
	if !strings.HasPrefix(op.OldestArchivedAt, "2026-05-30T00:00:00") {
		t.Fatalf("oldest=%q, want the whole-second row (lexical MIN would pick .5Z)", op.OldestArchivedAt)
	}
	if strings.Contains(op.OldestArchivedAt, ".5") {
		t.Fatalf("oldest=%q picked the later fractional row", op.OldestArchivedAt)
	}
	if op.Internal {
		t.Fatal("op-run must not be internal")
	}
	internal := byID["compact-copy-filter-abcd"]
	if !internal.Internal {
		t.Fatal("compact-copy-filter-abcd must be internal")
	}
}

func insertListArchiveRow(t *testing.T, db *sql.DB, runID, id, archivedAt string) {
	t.Helper()
	if _, err := db.Exec(`
INSERT INTO event_content_dedupe_archive
    (id, kind, client, agent, session_id, workspace, body, created_at, source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
VALUES (?, 'transcript', 'claude', 'claude', 's1', 'w1', 'body', ?, NULL, 'kept', ?, ?, 'g', 'duplicate')`,
		id, archivedAt, runID, archivedAt); err != nil {
		t.Fatal(err)
	}
}

// A row restored from retention keeps its ledger entry (restore only sets
// restored_at), and that entry holds an ON DELETE RESTRICT reference to
// events(id). Such a row looks entirely ordinary — body available, body intact —
// so nothing but the ledger itself distinguishes it. Archiving one raises a raw
// SQLite foreign-key error that aborts the batch mid-apply, so it must never
// reach the plan.
func TestStoreManagementDatasource_DedupeContentEvents_SkipsRetentionLedgerRows(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	rows := []struct{ id, createdAt string }{
		{"evt-l1", "2026-04-10T00:00:00Z"},
		{"evt-l2", "2026-04-10T00:00:02Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, 'prompt', 'codex', 's10', 'w1', 'restored body', ?, 'user_prompt_submit', 'hook')`,
			r.id, r.createdAt,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO raw_body_retention_executions (plan_id, status, candidate_count, pruned_count, started_at, completed_at)
		 VALUES ('plan-1', 'restored', 1, 1, '2026-04-09T00:00:00Z', '2026-04-09T00:00:01Z')`,
	); err != nil {
		t.Fatalf("insert execution error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO raw_body_retention_entries (plan_id, event_id, body_sha256, stored_bytes, pruned_at, restored_at)
		 VALUES ('plan-1', 'evt-l2', 'sha', 13, '2026-04-09T00:00:00Z', '2026-04-09T12:00:00Z')`,
	); err != nil {
		t.Fatalf("insert ledger entry error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-ledger",
		Now: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	for _, group := range result.Groups {
		for _, id := range group.DuplicateEventIDs {
			if id == "evt-l2" {
				t.Errorf("ledger-held row evt-l2 was selected as a duplicate of %s", group.KeptEventID)
			}
		}
	}

	verify, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = verify.Close() }()
	var survivors int
	if err := verify.QueryRow(`SELECT COUNT(*) FROM events WHERE id IN ('evt-l1','evt-l2')`).Scan(&survivors); err != nil {
		t.Fatalf("count ledger rows error = %v", err)
	}
	if survivors != 2 {
		t.Errorf("ledger rows surviving = %d, want 2", survivors)
	}
	// The rest of the apply must still have happened: an abort would have rolled
	// back the batch that carried group A.
	if diff := cmp.Diff(map[string][]string{
		"evt-a1": {"evt-a2", "evt-a3"},
		"evt-c1": {"evt-c2"},
	}, groupByKept(result)); diff != "" {
		t.Errorf("plan (-want +got):\n%s", diff)
	}
}

// A ledger-held row must stay visible to clustering even though it can never be
// archived. Three rows sit 9s apart inside the 10s proximity window and the
// middle one carries a retention ledger entry. Excluding it from the candidate
// scan -- the obvious way to avoid the ON DELETE RESTRICT abort -- widens the
// gap across it to 18s, splitting one cluster into two singletons and stranding
// an ordinary duplicate pair that has nothing to do with retention.
func TestStoreManagementDatasource_DedupeContentEvents_LedgerRowKeepsItsClusterIntact(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	rows := []struct{ id, createdAt string }{
		{"evt-m1", "2026-04-10T00:00:00Z"},
		{"evt-m2", "2026-04-10T00:00:09Z"},
		{"evt-m3", "2026-04-10T00:00:18Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, 'prompt', 'codex', 's11', 'w1', 'middle held body', ?, 'user_prompt_submit', 'hook')`,
			r.id, r.createdAt,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO raw_body_retention_executions (plan_id, status, candidate_count, pruned_count, started_at, completed_at)
		 VALUES ('plan-m', 'restored', 1, 1, '2026-04-09T00:00:00Z', '2026-04-09T00:00:01Z')`,
	); err != nil {
		t.Fatalf("insert execution error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO raw_body_retention_entries (plan_id, event_id, body_sha256, stored_bytes, pruned_at, restored_at)
		 VALUES ('plan-m', 'evt-m2', 'sha', 16, '2026-04-09T00:00:00Z', '2026-04-09T12:00:00Z')`,
	); err != nil {
		t.Fatalf("insert ledger entry error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-middle",
		Now: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	if diff := cmp.Diff(map[string][]string{
		"evt-a1": {"evt-a2", "evt-a3"},
		"evt-c1": {"evt-c2"},
		"evt-m1": {"evt-m3"},
	}, groupByKept(result)); diff != "" {
		t.Errorf("plan (-want +got):\n%s", diff)
	}

	verify, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = verify.Close() }()
	var survivors string
	if err := verify.QueryRow(
		`SELECT group_concat(id, ',') FROM (SELECT id FROM events WHERE id LIKE 'evt-m%' ORDER BY id)`,
	).Scan(&survivors); err != nil {
		t.Fatalf("read survivors error = %v", err)
	}
	if survivors != "evt-m1,evt-m2" {
		t.Errorf("survivors = %q, want evt-m1,evt-m2 (the ledger row stays, the duplicate goes)", survivors)
	}
}

func TestStoreManagementDatasource_DedupeContentEvents_DoesNotCascadeDeleteCommandAudit(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO command_audits(event_id, command_text, input_text, output_text) VALUES ('evt-c2', 'echo', '', '')`,
	); err != nil {
		t.Fatalf("attach command audit to transcript: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-audit",
		Now: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	for _, group := range result.Groups {
		for _, id := range group.DuplicateEventIDs {
			if id == "evt-c2" {
				t.Errorf("audit-held row evt-c2 was selected as a duplicate of %s", group.KeptEventID)
			}
		}
	}
	if !eventExists(t, dbPath, "evt-c2") {
		t.Fatal("audit-held transcript evt-c2 was deleted")
	}
	if !eventExists(t, dbPath, "evt-c1") {
		t.Fatal("canonical transcript evt-c1 was deleted")
	}

	verify, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = verify.Close() }()
	var audits int
	if err := verify.QueryRow(`SELECT COUNT(*) FROM command_audits WHERE event_id = 'evt-c2'`).Scan(&audits); err != nil {
		t.Fatalf("count command_audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("command_audits for evt-c2 = %d, want 1 (CASCADE must not fire)", audits)
	}
	if eventExists(t, dbPath, "evt-a2") || eventExists(t, dbPath, "evt-a3") {
		t.Fatal("ordinary prompt duplicates must still be archived")
	}
}

func TestStoreManagementDatasource_DedupeContentEvents_AuditHeldRowKeepsItsClusterIntact(t *testing.T) {
	t.Parallel()
	dbPath, storeManager, _ := seedDedupeFixture(t)

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	rows := []struct{ id, createdAt string }{
		{"evt-n1", "2026-04-10T00:00:00Z"},
		{"evt-n2", "2026-04-10T00:00:09Z"},
		{"evt-n3", "2026-04-10T00:00:18Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
			 VALUES (?, 'prompt', 'codex', 's12', 'w1', 'audit middle body', ?, 'user_prompt_submit', 'hook')`,
			r.id, r.createdAt,
		); err != nil {
			t.Fatalf("insert %s error = %v", r.id, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO command_audits(event_id, command_text, input_text, output_text) VALUES ('evt-n2', 'echo', '', '')`,
	); err != nil {
		t.Fatalf("attach command audit to middle row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	result, err := storeManager.DedupeContentEvents(context.Background(), apptypes.ContentEventDedupeParams{
		Agent: "codex", Apply: true, RunID: "dedupe-run-audit-cluster",
		Now: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("DedupeContentEvents(apply) error = %v", err)
	}
	if diff := cmp.Diff(map[string][]string{
		"evt-a1": {"evt-a2", "evt-a3"},
		"evt-c1": {"evt-c2"},
		"evt-n1": {"evt-n3"},
	}, groupByKept(result)); diff != "" {
		t.Errorf("plan (-want +got):\n%s", diff)
	}

	verify, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = verify.Close() }()
	var survivors string
	if err := verify.QueryRow(
		`SELECT group_concat(id, ',') FROM (SELECT id FROM events WHERE id LIKE 'evt-n%' ORDER BY id)`,
	).Scan(&survivors); err != nil {
		t.Fatalf("read survivors error = %v", err)
	}
	if survivors != "evt-n1,evt-n2" {
		t.Errorf("survivors = %q, want evt-n1,evt-n2 (the audit-held row stays, the duplicate goes)", survivors)
	}
	var audits int
	if err := verify.QueryRow(`SELECT COUNT(*) FROM command_audits WHERE event_id = 'evt-n2'`).Scan(&audits); err != nil {
		t.Fatalf("count command_audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("command_audits for evt-n2 = %d, want 1", audits)
	}
}
