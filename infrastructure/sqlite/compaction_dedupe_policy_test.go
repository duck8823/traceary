package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

func TestTrimDedupeArchiveComparesInstantsNotText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cutoff := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		archivedAt string
		wantKept   bool
	}{
		{name: "fractional second after cutoff", archivedAt: "2026-05-30T00:00:00.5Z", wantKept: true},
		{name: "just before cutoff", archivedAt: "2026-05-29T23:59:59.999999999Z", wantKept: false},
		{name: "exact cutoff boundary", archivedAt: "2026-05-30T00:00:00Z", wantKept: true},
		{name: "offset equal to cutoff", archivedAt: "2026-05-30T09:00:00+09:00", wantKept: true},
		{name: "unparseable", archivedAt: "not-a-time", wantKept: true},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.db")
	db, err := sql.Open("sqlite", directSQLiteRWDSNCreate(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(createDedupeArchiveTableSQL); err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		if _, err := db.Exec(`
INSERT INTO event_content_dedupe_archive
    (id, kind, client, agent, session_id, workspace, body, created_at, source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
VALUES (?, 'transcript', 'claude', 'claude', 'session-1', 'workspace-1', 'body', ?, NULL, 'kept-1', 'run-1', ?, 'group-1', 'duplicate')`,
			fmt.Sprintf("row-%d", i), tc.archivedAt, tc.archivedAt); err != nil {
			t.Fatal(err)
		}
	}
	trimmed, _, unparseable, err := trimDedupeArchive(ctx, db, cutoff, `length(CAST(body AS BLOB))`)
	if err != nil {
		t.Fatal(err)
	}
	if unparseable != 1 {
		t.Fatalf("unparseable=%d, want 1", unparseable)
	}
	if trimmed != 1 {
		t.Fatalf("trimmed=%d, want 1 (just-before row)", trimmed)
	}
	kept := map[string]bool{}
	rows, err := db.Query(`SELECT id FROM event_content_dedupe_archive`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		kept[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		id := fmt.Sprintf("row-%d", i)
		if kept[id] != tc.wantKept {
			t.Errorf("%s: kept=%v, want %v", tc.name, kept[id], tc.wantKept)
		}
	}
}

func TestCompactDoesNotCarryInternalDedupeArchiveIntoCandidate(t *testing.T) {
	ctx := context.Background()
	dbPath, dir := initPolicyStore(t)
	seedLargeDuplicateTranscripts(t, dbPath, 8, 3, 1<<20)
	svc := newPolicyCompaction(t, dbPath, dir)
	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Compact(ctx, application.CompactInput{Source: dbPath, Force: true, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	step, ok := result.Steps.Find(application.CompactStepDedupeArchive)
	if !ok {
		t.Fatalf("steps=%v, want dedupe_archive", result.Steps)
	}
	if step.Detail["internal_candidate_rows"] != 16 {
		t.Fatalf("internal_candidate_rows=%d, want 16", step.Detail["internal_candidate_rows"])
	}
	if step.Detail["internal_candidate_bytes"] < 16<<20 {
		t.Fatalf("internal_candidate_bytes=%d, want >= 16MiB", step.Detail["internal_candidate_bytes"])
	}
	if result.BytesAfter >= before.Size()-16<<20 {
		t.Fatalf("bytes_after=%d bytes_before=%d, want after < before-16MiB", result.BytesAfter, before.Size())
	}
	assertInternalArchiveCount(t, dbPath, 0)
	ids := storeEventIDs(t, dbPath)
	if len(ids) != 8 {
		t.Fatalf("events remaining=%d, want 8 survivors, got %v", len(ids), ids)
	}
	for i := 0; i < 8; i++ {
		want := fmt.Sprintf("keep-%d", i)
		if !containsID(ids, want) {
			t.Fatalf("survivor %s missing from %v", want, ids)
		}
		dup := fmt.Sprintf("dup-%d-1", i)
		if containsID(ids, dup) {
			t.Fatalf("duplicate %s still in events", dup)
		}
	}
}

func TestCompactRollbackRestoresIsolatedDuplicates(t *testing.T) {
	ctx := context.Background()
	dbPath, dir := initPolicyStore(t)
	seedLargeDuplicateTranscripts(t, dbPath, 8, 3, 1<<20)
	svc := newPolicyCompaction(t, dbPath, dir)
	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Compact(ctx, application.CompactInput{Source: dbPath, Force: true, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if _, err := svc.Rollback(ctx, result.Run.ID); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	ids := storeEventIDs(t, dbPath)
	if len(ids) != 24 {
		t.Fatalf("after rollback events=%d, want 24, got %v", len(ids), ids)
	}
	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("after rollback size=%d, want %d", after.Size(), before.Size())
	}
}

func TestCompactPreservesOperatorQuarantineWithinRetention(t *testing.T) {
	ctx := context.Background()
	dbPath, dir := initPolicyStore(t)
	now := time.Now().UTC()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	insertDedupeArchiveRow(t, db, "recent-1", now.Add(-10*24*time.Hour))
	insertDedupeArchiveRow(t, db, "old-1", now.Add(-100*24*time.Hour))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc := newPolicyCompaction(t, dbPath, dir)
	result, err := svc.Compact(ctx, application.CompactInput{Source: dbPath, Force: true, Now: now})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	step, ok := result.Steps.Find(application.CompactStepDedupeArchive)
	if !ok {
		t.Fatal("missing dedupe_archive step")
	}
	if step.Detail["retained_rows"] != 1 {
		t.Fatalf("retained_rows=%d, want 1", step.Detail["retained_rows"])
	}
	if step.Detail["trimmed_rows"] != 1 {
		t.Fatalf("trimmed_rows=%d, want 1", step.Detail["trimmed_rows"])
	}
	got := openCandidateArchiveIDs(t, dbPath)
	if len(got) != 1 || got[0] != "recent-1" {
		t.Fatalf("archive ids=%v, want [recent-1]", got)
	}
}

func TestCompactPurgesPriorInternalRunsWhenRollbackRetained(t *testing.T) {
	ctx := context.Background()
	dbPath, dir := initPolicyStore(t)
	now := time.Now().UTC()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	insertDedupeArchiveRowForRun(t, db, "compact-copy-filter-deadbeef", "prior-1", now.Add(-10*24*time.Hour))
	insertDedupeArchiveRow(t, db, "op-1", now.Add(-10*24*time.Hour))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc := newPolicyCompaction(t, dbPath, dir)
	result, err := svc.Compact(ctx, application.CompactInput{Source: dbPath, Force: true, Now: now})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	step, ok := result.Steps.Find(application.CompactStepDedupeArchive)
	if !ok {
		t.Fatal("missing dedupe_archive step")
	}
	if step.Detail["prior_internal_rows"] != 1 {
		t.Fatalf("prior_internal_rows=%d, want 1", step.Detail["prior_internal_rows"])
	}
	if step.Detail["retained_rows"] != 1 {
		t.Fatalf("retained_rows=%d, want 1 operator row", step.Detail["retained_rows"])
	}
	assertInternalArchiveCount(t, dbPath, 0)
	got := openCandidateArchiveIDs(t, dbPath)
	if len(got) != 1 || got[0] != "op-1" {
		t.Fatalf("archive ids=%v, want [op-1]", got)
	}
	runs := openCandidateArchiveRunIDs(t, dbPath)
	if len(runs) != 1 || runs[0] != "run-1" {
		t.Fatalf("archive run ids=%v, want [run-1]", runs)
	}
}

func TestCompactInPlaceSkipsInternalDedupe(t *testing.T) {
	ctx := context.Background()
	dbPath, _ := initPolicyStore(t)
	seedLargeDuplicateTranscripts(t, dbPath, 2, 2, 32)
	now := time.Now().UTC()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	insertDedupeArchiveRow(t, db, "old-op", now.Add(-100*24*time.Hour))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var steps application.CompactSteps
	filter := application.CompactFilter{
		ArchiveCutoff: now.Add(-application.DedupeArchiveRetention),
		OnStep:        func(step application.CompactStep) { steps = append(steps, step) },
	}
	if err := (SQLiteCompactionBuilder{}).CompactInPlace(ctx, dbPath, filter); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	step, ok := steps.Find(application.CompactStepDedupeArchive)
	if !ok {
		t.Fatalf("steps=%v, want dedupe_archive", steps)
	}
	if step.Skipped != application.DedupeArchiveSkipReason {
		t.Fatalf("skipped=%q, want %q", step.Skipped, application.DedupeArchiveSkipReason)
	}
	if step.Detail["trimmed_rows"] < 1 {
		t.Fatalf("trimmed_rows=%d, want retention trim", step.Detail["trimmed_rows"])
	}
	assertInternalArchiveCount(t, dbPath, 0)
	ids := storeEventIDs(t, dbPath)
	if len(ids) != 4 {
		t.Fatalf("in-place left %d events, want 4 duplicates retained, got %v", len(ids), ids)
	}
}

func TestVerifyPairRejectsCandidateDroppingOperatorQuarantine(t *testing.T) {
	ctx := context.Background()
	source, candidate := pairedPolicyStores(t)
	now := time.Now().UTC()
	cutoff := now.Add(-application.DedupeArchiveRetention)
	insertArchiveOn(t, source, "run-1", "keep-me", now.Add(-10*24*time.Hour))
	copyStore(t, source, candidate)
	deleteArchiveID(t, candidate, "keep-me")
	err := (SQLiteCompactionBuilder{Filter: application.CompactFilter{ArchiveCutoff: cutoff}}).VerifyPair(ctx, source, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want dropped operator row")
	}
	if !strings.Contains(err.Error(), "run-1") {
		t.Fatalf("VerifyPair() error = %v, want run-1 named", err)
	}
}

func TestVerifyPairRejectsInventedArchiveRow(t *testing.T) {
	ctx := context.Background()
	source, candidate := pairedPolicyStores(t)
	now := time.Now().UTC()
	insertArchiveOn(t, source, "run-1", "src-1", now)
	copyStore(t, source, candidate)
	insertArchiveOn(t, candidate, "run-1", "invented", now)
	err := (SQLiteCompactionBuilder{Filter: application.CompactFilter{ArchiveCutoff: now.Add(-application.DedupeArchiveRetention)}}).VerifyPair(ctx, source, candidate)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want invented row")
	}
	if !strings.Contains(err.Error(), "invented") {
		t.Fatalf("VerifyPair() error = %v, want invented id", err)
	}

	rewrittenSrc, rewrittenCand := pairedPolicyStores(t)
	insertArchiveOn(t, rewrittenSrc, "run-1", "src-1", now)
	copyStore(t, rewrittenSrc, rewrittenCand)
	db, err := sql.Open("sqlite", rewrittenCand)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE event_content_dedupe_archive SET reason = 'rewritten' WHERE id = 'src-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	checkpointStore(t, rewrittenCand)
	err = (SQLiteCompactionBuilder{Filter: application.CompactFilter{ArchiveCutoff: now.Add(-application.DedupeArchiveRetention)}}).VerifyPair(ctx, rewrittenSrc, rewrittenCand)
	if err == nil {
		t.Fatal("VerifyPair() error = nil, want rewritten row")
	}
	if !strings.Contains(err.Error(), "rewrote") {
		t.Fatalf("VerifyPair() error = %v, want rewrote", err)
	}
}

func TestVerifyPairAcceptsMissingInternalRunRows(t *testing.T) {
	ctx := context.Background()
	source, candidate := pairedPolicyStores(t)
	now := time.Now().UTC()
	insertArchiveOn(t, source, "compact-copy-filter-deadbeef", "int-1", now)
	copyStore(t, source, candidate)
	deleteArchiveID(t, candidate, "int-1")
	if err := (SQLiteCompactionBuilder{Filter: application.CompactFilter{ArchiveCutoff: now.Add(-application.DedupeArchiveRetention)}}).VerifyPair(ctx, source, candidate); err != nil {
		t.Fatalf("VerifyPair() error = %v, want accept missing internal rows", err)
	}
}

func TestClassifyCandidateWithZeroFilterIgnoresRetention(t *testing.T) {
	ctx := context.Background()
	source, candidate := pairedPolicyStores(t)
	now := time.Now().UTC()
	insertArchiveOn(t, source, "run-1", "old-1", now.Add(-100*24*time.Hour))
	copyStore(t, source, candidate)
	deleteArchiveID(t, candidate, "old-1")
	got, err := (SQLiteCompactionBuilder{}).ClassifyCandidate(ctx, source, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if got != domain.CandidateConditionComplete {
		t.Fatalf("ClassifyCandidate() = %s, want complete", got)
	}
}

func initPolicyStore(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "traceary.db")
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStoreManagementDatasource(NewDatabase(path, migrations)).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"event_search_documents", "event_search_fts", "event_search_backfill_state"} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	checkpointStore(t, path)
	return path, dir
}

func checkpointStore(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
}

func newPolicyCompaction(t *testing.T, dbPath, dir string) application.StoreCompactionUsecase {
	t.Helper()
	return usecase.NewStoreCompactionUsecase(
		dbPath,
		&CompactionFileJournal{Dir: filepath.Join(dir, ".traceary-compaction")},
		&SQLiteCompactionBuilder{},
		StoreReplacementFiles{CallerHoldsExclusiveLease: true},
		StoreLeaseCoordinator{},
	)
}

func seedLargeDuplicateTranscripts(t *testing.T, dbPath string, groups, copies, bodyBytes int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for g := 0; g < groups; g++ {
		body := make([]byte, bodyBytes)
		if _, err := rand.Read(body); err != nil {
			t.Fatal(err)
		}
		session := fmt.Sprintf("s-%d", g)
		for c := 0; c < copies; c++ {
			id := fmt.Sprintf("dup-%d-%d", g, c)
			if c == 0 {
				id = fmt.Sprintf("keep-%d", g)
			}
			created := base.Add(time.Duration(g)*time.Minute + time.Duration(c)*time.Second)
			if _, err := db.Exec(`
INSERT INTO events (id, kind, agent, session_id, workspace, body, created_at, source_hook, client)
VALUES (?, 'transcript', 'codex', ?, 'w1', ?, ?, 'stop', 'hook')`,
				id, session, body, formatTimestamp(created)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func storeEventIDs(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func assertInternalArchiveCount(t *testing.T, dbPath string, want int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int64
	if err := db.QueryRow(`
SELECT COUNT(*) FROM event_content_dedupe_archive
 WHERE dedupe_run_id >= 'compact-copy-filter-' AND dedupe_run_id < 'compact-copy-filter.'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("internal archive rows=%d, want %d", n, want)
	}
}

func pairedPolicyStores(t *testing.T) (source, candidate string) {
	t.Helper()
	source, _ = initPolicyStore(t)
	candidate = filepath.Join(t.TempDir(), "candidate.db")
	copyStore(t, source, candidate)
	return source, candidate
}

func copyStore(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func insertArchiveOn(t *testing.T, path, runID, id string, archivedAt time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	insertDedupeArchiveRowForRun(t, db, runID, id, archivedAt)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	checkpointStore(t, path)
}

func deleteArchiveID(t *testing.T, path, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM event_content_dedupe_archive WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	checkpointStore(t, path)
}
