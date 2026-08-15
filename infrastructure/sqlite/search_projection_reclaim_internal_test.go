package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestSearchProjectionFTSReclaimKeepsConstantLargeCorpusBounded(t *testing.T) {
	tests := []struct {
		name        string
		generations int
	}{
		{name: "ten churn generations", generations: 10},
	}
	const (
		steadyStateGeneration = 2
		steadyStateTolerance  = 0.05
		maxFinalToInitial     = 2.0
	)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.db")
			migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
			if err != nil {
				t.Fatal(err)
			}
			store := NewDatabase(path, migrations)
			if err = store.initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			body := "Traceary projection payload with searchable trigrams. " + strings.Repeat("representative indexed text ", 1800)
			insertCorpus := func(generation int) {
				t.Helper()
				tx, txErr := db.Begin()
				if txErr != nil {
					t.Fatal(txErr)
				}
				for i := 0; i < 220; i++ {
					id := generation*220 + i + 1
					if _, txErr = tx.Exec(`INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, "fixture", id, fmt.Sprintf("event-%d", id), "2026-08-10T00:00:00Z", body, len(body)); txErr != nil {
						_ = tx.Rollback()
						t.Fatal(txErr)
					}
				}
				if txErr = tx.Commit(); txErr != nil {
					t.Fatal(txErr)
				}
			}
			shadowBytes := func() int64 {
				t.Helper()
				var bytes int64
				if err = db.QueryRow(`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name LIKE 'search_projection_recent_fts_%'`).Scan(&bytes); err != nil {
					t.Fatal(err)
				}
				return bytes
			}
			insertCorpus(0)
			sizes := []int64{shadowBytes()}
			for generation := 1; generation <= tt.generations; generation++ {
				if _, err = db.Exec(`DELETE FROM search_projection_recent_documents WHERE generation_id='fixture'`); err != nil {
					t.Fatal(err)
				}
				insertCorpus(generation)
				if err = reclaimSearchProjectionFTS(context.Background(), db, apptypes.DefaultSearchProjectionLockTime); err != nil {
					t.Fatal(err)
				}
				sizes = append(sizes, shadowBytes())
			}
			steadyState := float64(sizes[steadyStateGeneration])
			for generation := steadyStateGeneration + 1; generation < len(sizes); generation++ {
				deviation := (float64(sizes[generation]) - steadyState) / steadyState
				if deviation < -steadyStateTolerance || deviation > steadyStateTolerance {
					t.Fatalf("shadow size generation %d deviated %.1f%% from steady state beyond %.0f%% tolerance: sizes=%v", generation, deviation*100, steadyStateTolerance*100, sizes)
				}
			}
			if finalToInitial := float64(sizes[len(sizes)-1]) / float64(sizes[0]); finalToInitial > maxFinalToInitial {
				t.Fatalf("final shadow size is %.2fx the initial size, exceeds %.1fx bound: sizes=%v", finalToInitial, maxFinalToInitial, sizes)
			}
			// Measured with one transaction per step on the production 250 ms
			// budget: 11,395,072 initially, 13,733,888 after generation 1,
			// 19,509,248 at steady state, then 19,505,152-19,513,344 through
			// generation 10. The 5% tolerance and 2.0x bound are deliberate
			// limits, not a proxy for monotonic growth.
			t.Logf("shadow-table sizes before/after churn: %v", sizes)
		})
	}
}

func TestSearchProjectionStatusReportsFTSShadowLogicalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(path, migrations)
	if err = store.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	budget := projectionBudget()
	generation, err := store.Start(context.Background(), budget, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`UPDATE search_projection_state SET active_generation_id=? WHERE singleton=1`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("representative indexed text ", 1800)
	for i := 0; i < 220; i++ {
		if _, err = db.Exec(`INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, generation.GenerationID, i+1, fmt.Sprintf("event-%d", i), "2026-08-10T00:00:00Z", body, len(body)); err != nil {
			t.Fatal(err)
		}
	}
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(true, status.FTSLogicalBytes > status.RecentBytes); diff != "" {
		t.Fatalf("FTS logical bytes must describe shadow tables, not source text (-want +got):\n%s", diff)
	}
}

func TestSearchProjectionFTSLogicalBytesUsesIntegerColumnWidth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(path, migrations)
	if err = store.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	generation, err := store.Start(context.Background(), projectionBudget(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`UPDATE search_projection_state SET active_generation_id=? WHERE singleton=1`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("representative indexed text ", 1800)
	for i := 0; i < 220; i++ {
		if _, err = db.Exec(`INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, generation.GenerationID, i+1, fmt.Sprintf("event-%d", i), "2026-08-10T00:00:00Z", body, len(body)); err != nil {
			t.Fatal(err)
		}
	}
	want, digitCount := measureSearchProjectionFTSLogicalBytes(t, db)
	if want <= 0 {
		t.Fatal("expected populated FTS shadow tables")
	}
	if digitCount == want {
		t.Fatal("digit-count SUM matched the 8-byte integer rule; fixture cannot prove the integer-column fix")
	}
	status, err := store.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, status.FTSLogicalBytes); diff != "" {
		t.Fatalf("fts_logical_bytes must use 8-byte integer width (-want +got):\n%s", diff)
	}
}

const searchProjectionFTSIntegerWidth = 8

func measureSearchProjectionFTSLogicalBytes(t *testing.T, db *sql.DB) (logical int64, digitCount int64) {
	t.Helper()
	rows, err := db.Query(`SELECT block FROM search_projection_recent_fts_data`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var block []byte
		if err = rows.Scan(&block); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		n := int64(len(block))
		logical += n
		digitCount += n
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT segid, term, pgno FROM search_projection_recent_fts_idx`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var segid, pgno int64
		var term []byte
		if err = rows.Scan(&segid, &term, &pgno); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		logical += searchProjectionFTSIntegerWidth + int64(len(term)) + searchProjectionFTSIntegerWidth
		digitCount += decimalDigitCount(segid) + int64(len(term)) + decimalDigitCount(pgno)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT id, sz FROM search_projection_recent_fts_docsize`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var sz []byte
		if err = rows.Scan(&id, &sz); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		logical += searchProjectionFTSIntegerWidth + int64(len(sz))
		digitCount += decimalDigitCount(id) + int64(len(sz))
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT k, typeof(v), v FROM search_projection_recent_fts_config`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, valueType string
		var value any
		if err = rows.Scan(&key, &valueType, &value); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		logical += int64(len(key))
		digitCount += int64(len(key))
		if valueType == "integer" {
			logical += searchProjectionFTSIntegerWidth
			n, ok := value.(int64)
			if !ok {
				_ = rows.Close()
				t.Fatalf("config v typeof=integer but %T", value)
			}
			digitCount += decimalDigitCount(n)
			continue
		}
		n := blobOrTextLength(value)
		logical += n
		digitCount += n
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	return logical, digitCount
}

func decimalDigitCount(n int64) int64 {
	if n < 0 {
		return 1 + decimalDigitCount(-n)
	}
	if n == 0 {
		return 1
	}
	var digits int64
	for n > 0 {
		digits++
		n /= 10
	}
	return digits
}

func blobOrTextLength(v any) int64 {
	switch x := v.(type) {
	case []byte:
		return int64(len(x))
	case string:
		return int64(len(x))
	default:
		return 0
	}
}

func TestSearchProjectionApplySuccessRemainsProgressAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	want := apptypes.SearchProjectionProgress{Selected: 1, Completed: true, GenerationID: "committed"}
	got, retry, err := classifySearchProjectionApplyResult(want, nil)
	if retry || err != nil || got != want {
		t.Fatalf("classified committed batch = %+v retry=%v err=%v, want progress", got, retry, err)
	}
	if err := ctx.Err(); err == nil {
		t.Fatal("test context did not expire")
	}
}

func TestSearchProjectionFailingReclaimDoesNotFailCommittedBatch(t *testing.T) {
	ctx := context.Background()
	originalReclaim := reclaimSearchProjectionFTSFn
	reclaimSearchProjectionFTSFn = func(context.Context, *sql.DB, time.Duration) error {
		return errors.New("reclaim failed in test")
	}
	defer func() { reclaimSearchProjectionFTSFn = originalReclaim }()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := fs.Sub(os.DirFS("../.."), "schema/sqlite/migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDatabase(path, migrations)
	if err = store.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	budget := projectionBudget()
	generation, err := store.Start(ctx, budget, now)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`UPDATE search_projection_state SET phase='cleanup',checkpoint=0 WHERE generation_id=?`, generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	largeBody := strings.Repeat("cleanup reclaim measurement text ", 6000)
	for i := 0; i < 220; i++ {
		if _, err = db.Exec(`INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, generation.GenerationID, i+1, fmt.Sprintf("event-%d", i), formatTimestamp(now), largeBody, len(largeBody)); err != nil {
			t.Fatal(err)
		}
	}
	var finalProgress apptypes.SearchProjectionProgress
	for i := 0; i < 3; i++ {
		plan := apptypes.ProjectionBatchPlan{
			GenerationID: generation.GenerationID, Phase: "cleanup", ExpectedRevision: generation.SourceRevision,
			ExpectedCheckpoint: int64(i), NextCheckpoint: int64(i + 1),
			Cleanup: []apptypes.ProjectionCleanupCandidate{{Class: "recent", RowID: int64(i + 1), LogicalBytes: 7}},
		}
		if i == 2 {
			plan.Completed = true
			plan.NextPhase = "complete"
			plan.FinalState = "complete"
		}
		progress, applyErr := store.CleanupBatch(ctx, plan, 80*time.Millisecond, now)
		if diff := cmp.Diff(nil, applyErr); diff != "" {
			t.Fatalf("cleanup batch %d returned reclaim failure (-want +got):\n%s", i, diff)
		}
		var noProgress *apptypes.SearchProjectionNoProgressError
		if errors.As(applyErr, &noProgress) {
			t.Fatalf("cleanup batch %d returned no-progress error: %v", i, noProgress)
		}
		finalProgress = progress
		if i < 2 && progress.Completed {
			t.Fatalf("intermediate cleanup batch %d completed generation: %+v", i, progress)
		}
	}
	var state, phase string
	if err = db.QueryRow(`SELECT state,phase FROM search_projection_state WHERE generation_id=?`, generation.GenerationID).Scan(&state, &phase); err != nil {
		t.Fatal(err)
	}
	if state != "complete" || phase != "complete" {
		t.Fatalf("generation state=%s phase=%s, want complete/complete", state, phase)
	}
	if diff := cmp.Diff(apptypes.SearchProjectionProgress{Selected: 0, Evicted: 1, Cleaned: 1, CleanupBytes: 7, Completed: true, GenerationID: generation.GenerationID}, finalProgress); diff != "" {
		t.Fatalf("final cleanup progress (-want +got):\n%s", diff)
	}
}
