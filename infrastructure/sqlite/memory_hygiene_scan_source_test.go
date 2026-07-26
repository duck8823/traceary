package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

func TestMemoryHygieneRevision_AdvancesForEveryMemoryMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "revision.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()

	if got := memoryHygieneRevisionForTest(t, db); got != 0 {
		t.Fatalf("initial revision = %d, want 0", got)
	}
	insertMemoryHygieneRows(t, db, 1, "same fact")
	if got := memoryHygieneRevisionForTest(t, db); got != 1 {
		t.Fatalf("revision after insert = %d, want 1", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE memories SET fact = ? WHERE id = ?`, "updated fact", "mem-0000"); err != nil {
		t.Fatalf("UPDATE memories error = %v", err)
	}
	if got := memoryHygieneRevisionForTest(t, db); got != 2 {
		t.Fatalf("revision after update = %d, want 2", got)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, "mem-0000"); err != nil {
		t.Fatalf("DELETE memories error = %v", err)
	}
	if got := memoryHygieneRevisionForTest(t, db); got != 3 {
		t.Fatalf("revision after delete = %d, want 3", got)
	}

	page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
		Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
		MaxRows:        2,
		MaxScanBytes:   1_024,
		MaxComparisons: 1,
	})
	if err != nil {
		t.Fatalf("ScanMemoryHygienePage() error = %v", err)
	}
	if !page.Done {
		t.Fatalf("empty read-only scan Done = false, page = %#v", page)
	}
	if got := memoryHygieneRevisionForTest(t, db); got != 3 {
		t.Fatalf("read-only scan changed revision to %d, want 3", got)
	}
}

func TestMemoryHygieneScanSource_RejectsRevisionChangedBetweenPages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "stale.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 2, "fact")

	first, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
		Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
		MaxRows:        1,
		MaxScanBytes:   1 << 20,
		MaxComparisons: 1,
	})
	if err != nil {
		t.Fatalf("first ScanMemoryHygienePage() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE memories SET fact = ? WHERE id = ?`, "changed", "mem-0001"); err != nil {
		t.Fatalf("UPDATE memories error = %v", err)
	}
	_, err = source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
		Phase:            apptypes.MemoryHygieneScanPhaseAcceptedRows,
		Keyset:           first.ProgressKeyset,
		ExpectedRevision: domtypes.Some(first.Revision),
		MaxRows:          1,
		MaxScanBytes:     1 << 20,
		MaxComparisons:   1,
	})
	if !errors.Is(err, queryservice.ErrMemoryHygieneRevisionChanged) {
		t.Fatalf("continuation error = %v, want ErrMemoryHygieneRevisionChanged", err)
	}
}

func TestMemoryHygieneScanSource_SimilarityPairsResumeWithoutDuplicateOrSkip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pairs.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 3, "shared words")

	keyset := apptypes.MemoryHygieneScanKeyset{}
	expectedRevision := domtypes.None[int64]()
	pairs := make([]string, 0, 3)
	for invocation := 0; invocation < 10; invocation++ {
		page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
			Phase:            apptypes.MemoryHygieneScanPhaseSimilarityPairs,
			Keyset:           keyset,
			ExpectedRevision: expectedRevision,
			MaxRows:          4,
			MaxScanBytes:     1 << 20,
			MaxComparisons:   1,
		})
		if err != nil {
			t.Fatalf("ScanMemoryHygienePage(invocation=%d) error = %v", invocation, err)
		}
		if _, ok := expectedRevision.Value(); !ok {
			expectedRevision = domtypes.Some(page.Revision)
		}
		for _, unit := range page.Units {
			peer, ok := unit.Peer.Value()
			if !ok {
				continue
			}
			pairs = append(pairs, unit.Row.MemoryID().String()+"-"+peer.MemoryID().String())
		}
		keyset = page.ProgressKeyset
		if page.Done {
			break
		}
		if page.StopReason != apptypes.MemoryHygieneStopReasonComparisonLimit {
			t.Fatalf("partial pair page stop reason = %q, want comparison_limit", page.StopReason)
		}
	}
	want := []string{"mem-0000-mem-0001", "mem-0000-mem-0002", "mem-0001-mem-0002"}
	if fmt.Sprint(pairs) != fmt.Sprint(want) {
		t.Fatalf("pairs = %v, want %v", pairs, want)
	}
}

func TestMemoryHygieneScanSource_ExactDuplicateMembersResumeOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "duplicates.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 3, "same exact fact")

	keyset := apptypes.MemoryHygieneScanKeyset{}
	expectedRevision := domtypes.None[int64]()
	members := make([]string, 0, 3)
	for invocation := 0; invocation < 10; invocation++ {
		page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
			Phase:            apptypes.MemoryHygieneScanPhaseExactDuplicates,
			Keyset:           keyset,
			ExpectedRevision: expectedRevision,
			MaxRows:          1,
			MaxScanBytes:     1 << 20,
			MaxComparisons:   1,
		})
		if err != nil {
			t.Fatalf("ScanMemoryHygienePage(invocation=%d) error = %v", invocation, err)
		}
		if _, ok := expectedRevision.Value(); !ok {
			expectedRevision = domtypes.Some(page.Revision)
		}
		for _, unit := range page.Units {
			if _, ok := unit.RelatedMemoryID.Value(); !ok {
				t.Fatalf("duplicate member %s has no deterministic peer", unit.Row.MemoryID())
			}
			members = append(members, unit.Row.MemoryID().String())
		}
		keyset = page.ProgressKeyset
		if page.Done {
			break
		}
	}
	want := []string{"mem-0000", "mem-0001", "mem-0002"}
	if fmt.Sprint(members) != fmt.Sprint(want) {
		t.Fatalf("duplicate members = %v, want %v", members, want)
	}
}

func TestMemoryHygieneScanSource_LargeScopeHonorsFixedInvocationBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "large.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 256, "same scope fact")

	page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
		Phase:          apptypes.MemoryHygieneScanPhaseSimilarityPairs,
		MaxRows:        16,
		MaxScanBytes:   1 << 20,
		MaxComparisons: 7,
	})
	if err != nil {
		t.Fatalf("ScanMemoryHygienePage() error = %v", err)
	}
	if page.Done {
		t.Fatal("large-scope page Done = true, want bounded partial page")
	}
	if page.StopReason != apptypes.MemoryHygieneStopReasonComparisonLimit {
		t.Fatalf("StopReason = %q, want comparison_limit", page.StopReason)
	}
	if page.Comparisons != 7 || len(page.Units) != 7 || page.ScannedRows != 14 {
		t.Fatalf("usage = comparisons:%d units:%d rows:%d, want 7/7/14", page.Comparisons, len(page.Units), page.ScannedRows)
	}
}

func TestMemoryHygieneScanSource_SingletonScopesChargeAnchorRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "singleton-scopes.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 4, "independent fact")
	for i := 0; i < 4; i++ {
		if _, err := db.ExecContext(
			ctx,
			`UPDATE memories SET scope_value = ? WHERE id = ?`,
			fmt.Sprintf("github.com/example/repo-%d", i),
			fmt.Sprintf("mem-%04d", i),
		); err != nil {
			t.Fatalf("move singleton %d error = %v", i, err)
		}
	}

	page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
		Phase:          apptypes.MemoryHygieneScanPhaseSimilarityPairs,
		MaxRows:        2,
		MaxScanBytes:   1 << 20,
		MaxComparisons: 10,
	})
	if err != nil {
		t.Fatalf("ScanMemoryHygienePage() error = %v", err)
	}
	if page.StopReason != apptypes.MemoryHygieneStopReasonRowLimit || page.ScannedRows != 2 || len(page.Units) != 2 {
		t.Fatalf("singleton page = stop:%q rows:%d units:%d, want row_limit/2/2",
			page.StopReason,
			page.ScannedRows,
			len(page.Units),
		)
	}
	if page.Comparisons != 0 {
		t.Fatalf("singleton comparisons = %d, want 0", page.Comparisons)
	}
}

func TestMemoryHygieneScanSource_OversizedFactStopsBeforeReturningRawRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "oversized.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 1, strings.Repeat("secret-tail-", 1_000))

	page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
		Phase:          apptypes.MemoryHygieneScanPhaseAcceptedRows,
		MaxRows:        2,
		MaxScanBytes:   64,
		MaxComparisons: 1,
	})
	if err != nil {
		t.Fatalf("ScanMemoryHygienePage() error = %v", err)
	}
	if page.StopReason != apptypes.MemoryHygieneStopReasonScanByteLimit {
		t.Fatalf("StopReason = %q, want scan_byte_limit", page.StopReason)
	}
	if len(page.Units) != 0 || page.ScannedBytes != 0 || page.ProgressKeyset != (apptypes.MemoryHygieneScanKeyset{}) {
		t.Fatalf("oversized row crossed source boundary: %#v", page)
	}
}

func TestMemoryHygieneScanSource_TargetedRevalidationFailsClosedAtPeerBound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "revalidate.db")
	source, store := newMemoryDatasource(t, dbPath, onDiskSQLiteMigrations(t))
	if err := store.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(t, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(t, db, 4, "same exact fact")
	if _, err := db.ExecContext(
		ctx,
		`UPDATE memories SET scope_value = ? WHERE id = ?`,
		"github.com/example/other",
		"mem-0003",
	); err != nil {
		t.Fatalf("move unrelated peer error = %v", err)
	}

	result, err := source.RevalidateMemoryHygiene(ctx, apptypes.MemoryHygieneRevalidationCriteria{
		MemoryID:       domtypes.MemoryID("mem-0000"),
		MaxRows:        2,
		MaxScanBytes:   1 << 20,
		MaxComparisons: 10,
	})
	if err != nil {
		t.Fatalf("RevalidateMemoryHygiene() error = %v", err)
	}
	if result.Complete || result.StopReason != apptypes.MemoryHygieneStopReasonRowLimit {
		t.Fatalf("bounded revalidation = complete:%t stop:%q, want partial row_limit", result.Complete, result.StopReason)
	}
	if result.ScannedRows != 2 || len(result.Peers) != 1 {
		t.Fatalf("bounded revalidation rows=%d peers=%d, want 2/1", result.ScannedRows, len(result.Peers))
	}
	if peerID, ok := result.ExactDuplicateMemoryID.Value(); !ok || peerID.String() != "mem-0001" {
		t.Fatalf("ExactDuplicateMemoryID = (%s,%t), want mem-0001", peerID, ok)
	}

	complete, err := source.RevalidateMemoryHygiene(ctx, apptypes.MemoryHygieneRevalidationCriteria{
		MemoryID:       domtypes.MemoryID("mem-0000"),
		MaxRows:        10,
		MaxScanBytes:   1 << 20,
		MaxComparisons: 10,
	})
	if err != nil {
		t.Fatalf("complete RevalidateMemoryHygiene() error = %v", err)
	}
	if !complete.Complete || complete.StopReason != apptypes.MemoryHygieneStopReasonComplete {
		t.Fatalf("complete revalidation = %#v", complete)
	}
	if len(complete.Peers) != 2 {
		t.Fatalf("same-scope peers = %d, want 2 (unrelated scope excluded)", len(complete.Peers))
	}
}

func BenchmarkMemoryHygieneScanSource_LargeScopeFixedBudget(b *testing.B) {
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "benchmark.db")
	database := infra.NewDatabase(dbPath, onDiskSQLiteMigrations(b))
	source := infra.NewMemoryDatasource(database)
	store := infra.NewStoreManagementDatasource(database)
	if err := store.Initialize(ctx); err != nil {
		b.Fatalf("Initialize() error = %v", err)
	}
	db := openMemoryHygieneTestDB(b, dbPath)
	defer func() { _ = db.Close() }()
	insertMemoryHygieneRows(b, db, 1_000, "benchmark fact")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := source.ScanMemoryHygienePage(ctx, apptypes.MemoryHygieneScanPageCriteria{
			Phase:          apptypes.MemoryHygieneScanPhaseSimilarityPairs,
			MaxRows:        128,
			MaxScanBytes:   1 << 20,
			MaxComparisons: 64,
		})
		if err != nil {
			b.Fatalf("ScanMemoryHygienePage() error = %v", err)
		}
		if page.Comparisons != 64 {
			b.Fatalf("Comparisons = %d, want 64", page.Comparisons)
		}
	}
}

func openMemoryHygieneTestDB(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return db
}

func memoryHygieneRevisionForTest(t testing.TB, db *sql.DB) int64 {
	t.Helper()
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM memory_hygiene_revision WHERE singleton = 1`).Scan(&revision); err != nil {
		t.Fatalf("query memory hygiene revision error = %v", err)
	}
	return revision
}

func insertMemoryHygieneRows(t testing.TB, db *sql.DB, count int, fact string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
INSERT INTO memories (
    id, type, scope_kind, scope_value, fact, status, confidence, source,
    valid_from, created_at, updated_at
) VALUES (?, 'decision', 'workspace', 'github.com/duck8823/traceary', ?,
          'accepted', 'verified', 'manual',
          '2026-07-01T00:00:00.000000000Z',
          '2026-07-01T00:00:00Z',
          '2026-07-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer func() { _ = stmt.Close() }()
	for i := 0; i < count; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("mem-%04d", i), fact); err != nil {
			t.Fatalf("insert memory %d error = %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

var _ interface {
	ScanMemoryHygienePage(context.Context, apptypes.MemoryHygieneScanPageCriteria) (apptypes.MemoryHygieneScanSourcePage, error)
} = (*infra.MemoryDatasource)(nil)
