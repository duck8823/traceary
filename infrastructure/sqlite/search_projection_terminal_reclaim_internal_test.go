package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

func seedDerivedRows(t *testing.T, db *sql.DB, generation string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		eventID := fmt.Sprintf("%s-e-%04d", generation, i)
		if _, err := db.ExecContext(ctx, `INSERT INTO search_projection_source_sequence(event_id) VALUES(?)`, eventID); err != nil {
			t.Fatalf("source sequence: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO search_projection_session_keywords(generation_id,session_id,keyword,occurrences,keyword_version)
VALUES(?,?,?,1,1)`, generation, "sess-cap", fmt.Sprintf("kw-%04d", i)); err != nil {
			t.Fatalf("keyword: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version)
SELECT ?, sequence, ?, randomblob(16), 1 FROM search_projection_source_sequence WHERE event_id=?`, generation, eventID, eventID); err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
	}
}

func countGenerationRows(t *testing.T, db *sql.DB, generation string) int64 {
	t.Helper()
	var keywords, fingerprints int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords WHERE generation_id=?`, generation).Scan(&keywords); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM literal_search_fingerprints WHERE generation_id=?`, generation).Scan(&fingerprints); err != nil {
		t.Fatal(err)
	}
	return keywords + fingerprints
}

func TestTerminalReclaimBoundsResidentRowsAcrossRepeatedFailures(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	budget := defaultSearchProjectionCatchUpBudget()
	budget.LockTime = 5 * time.Second

	cycle := func(kind string) (string, int64, int) {
		t.Helper()
		gen, err := store.Start(ctx, budget, now)
		if err != nil {
			t.Fatal(err)
		}
		seedDerivedRows(t, db, gen.GenerationID, 250)
		switch kind {
		case "fail":
			if err := store.MarkFailed(ctx, gen.GenerationID, gen.SourceRevision, "oversize_row", now); err != nil {
				t.Fatal(err)
			}
		case "abandon":
			if _, err := store.AbandonSearchProjection(ctx, now); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unknown cycle %s", kind)
		}
		pages := 0
		for {
			progress, pageErr := store.ReclaimTerminalGenerationPage(ctx, gen.GenerationID, budget, 200, now)
			if pageErr != nil {
				t.Fatalf("reclaim page: %v", pageErr)
			}
			pages++
			if progress.Done {
				break
			}
			if pages > 50 {
				t.Fatal("reclaim did not finish")
			}
		}
		var reclaimedAt string
		var reclaimedRows int64
		if err := db.QueryRow(`SELECT reclaimed_at, reclaimed_rows FROM search_projection_generation_lifecycle WHERE generation_id=?`, gen.GenerationID).Scan(&reclaimedAt, &reclaimedRows); err != nil {
			t.Fatal(err)
		}
		if reclaimedAt == "" {
			t.Fatal("reclaimed_at is empty")
		}
		if reclaimedRows != 500 {
			t.Fatalf("reclaimed_rows=%d, want 500", reclaimedRows)
		}
		if got := countGenerationRows(t, db, gen.GenerationID); got != 0 {
			t.Fatalf("resident rows=%d, want 0", got)
		}
		return gen.GenerationID, reclaimedRows, pages
	}

	_, _, pages1 := cycle("fail")
	after1 := countGenerationRows(t, db, "")
	_ = after1
	var totalAfter1 int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords`).Scan(&totalAfter1); err != nil {
		t.Fatal(err)
	}
	if pages1 < 2 {
		t.Fatalf("pages=%d, want bounded paging > 1", pages1)
	}

	_, _, pages2 := cycle("abandon")
	var totalAfter2 int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_projection_session_keywords`).Scan(&totalAfter2); err != nil {
		t.Fatal(err)
	}
	if totalAfter2 > totalAfter1 {
		t.Fatalf("resident keywords grew: after1=%d after2=%d", totalAfter1, totalAfter2)
	}
	if pages2 < 2 {
		t.Fatalf("second cycle pages=%d, want > 1", pages2)
	}
}

func TestTerminalReclaimRefusesActiveAndCompleteGenerations(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water,failure_class,terminal_at)
		VALUES('gen-complete','complete','h',0,1,'',''),
		      ('gen-active','failed','h',0,1,'oversize_row','2026-08-01T00:00:00Z');
		UPDATE search_projection_state
		   SET generation_id='gen-active', active_generation_id='gen-active', state='failed', phase='complete'
		 WHERE singleton=1;
	`); err != nil {
		t.Fatal(err)
	}
	seedDerivedRows(t, db, "gen-complete", 3)
	seedDerivedRows(t, db, "gen-active", 3)
	beforeComplete := countGenerationRows(t, db, "gen-complete")
	beforeActive := countGenerationRows(t, db, "gen-active")

	_, err := reclaimTerminalGenerationPageOn(ctx, db, "gen-active", 200, now)
	var drift *apptypes.SearchProjectionDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("active reclaim err=%v, want drift", err)
	}
	_, err = reclaimTerminalGenerationPageOn(ctx, db, "gen-complete", 200, now)
	if !errors.As(err, &drift) {
		t.Fatalf("complete reclaim err=%v, want drift", err)
	}
	if got := countGenerationRows(t, db, "gen-complete"); got != beforeComplete {
		t.Fatalf("complete rows changed: %d -> %d", beforeComplete, got)
	}
	if got := countGenerationRows(t, db, "gen-active"); got != beforeActive {
		t.Fatalf("active rows changed: %d -> %d", beforeActive, got)
	}
	_ = store
}

func TestTerminalReclaimResumesAfterInterruptedPage(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	budget := defaultSearchProjectionCatchUpBudget()
	budget.LockTime = 5 * time.Second
	gen, err := store.Start(ctx, budget, now)
	if err != nil {
		t.Fatal(err)
	}
	seedDerivedRows(t, db, gen.GenerationID, 250)
	if err := store.MarkFailed(ctx, gen.GenerationID, gen.SourceRevision, "oversize_row", now); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	progress, err := store.ReclaimTerminalGenerationPage(cancelCtx, gen.GenerationID, budget, 200, now)
	cancel()
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if progress.Done {
		t.Fatal("first page finished the generation")
	}
	for {
		progress, err = store.ReclaimTerminalGenerationPage(ctx, gen.GenerationID, budget, 200, now)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Done {
			break
		}
	}
	var reclaimedRows int64
	if err := db.QueryRow(`SELECT reclaimed_rows FROM search_projection_generation_lifecycle WHERE generation_id=?`, gen.GenerationID).Scan(&reclaimedRows); err != nil {
		t.Fatal(err)
	}
	if reclaimedRows != 500 {
		t.Fatalf("reclaimed_rows=%d, want 500 (no double count)", reclaimedRows)
	}
}

func TestTerminalReclaimUsesPrimaryKeySeek(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	ctx := context.Background()
	queries := []string{
		`SELECT rowid FROM search_projection_recent_documents WHERE generation_id = ? LIMIT ?`,
		`SELECT rowid FROM search_projection_session_summaries WHERE generation_id = ? LIMIT ?`,
		`SELECT rowid FROM search_projection_command_aggregates WHERE generation_id = ? LIMIT ?`,
		`SELECT rowid FROM search_projection_session_keywords WHERE generation_id = ? LIMIT ?`,
		`SELECT rowid FROM literal_search_fingerprints WHERE generation_id = ? LIMIT ?`,
		`SELECT rowid FROM search_projection_exclusions WHERE generation_id = ? LIMIT ?`,
	}
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, "g", 10)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		joined := strings.Join(plan, "\n")
		if !strings.Contains(joined, "PRIMARY KEY") && !strings.Contains(joined, "USING INDEX") && !strings.Contains(joined, "COVERING INDEX") {
			t.Fatalf("plan for %s does not seek: %s", query, joined)
		}
		if strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("plan uses TEMP B-TREE: %s", joined)
		}
	}
	_ = store
}

func TestSearchProjectionStatusReportsTerminalGenerationRows(t *testing.T) {
	store, db := newCapacityTestStore(t, nil)
	ctx := context.Background()
	if _, err := db.Exec(`
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water,failure_class,terminal_at)
		VALUES('term-a','failed','h',0,1,'oversize_row','2026-08-01T00:00:00Z'),
		      ('term-b','abandoned','h',0,1,'abandoned','2026-08-02T00:00:00Z');
		UPDATE search_projection_state SET generation_id='', active_generation_id='', state='failed', phase='complete' WHERE singleton=1;
	`); err != nil {
		t.Fatal(err)
	}
	seedDerivedRows(t, db, "term-a", 2)
	seedDerivedRows(t, db, "term-b", 3)
	status, err := store.SearchProjectionStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.TerminalGenerations != 2 {
		t.Fatalf("TerminalGenerations=%d, want 2", status.TerminalGenerations)
	}
	if status.KeywordRows != 0 || status.FingerprintRows != 0 {
		t.Fatalf("active-scoped rows keyword=%d fingerprint=%d, want 0", status.KeywordRows, status.FingerprintRows)
	}
	if status.TerminalKeywordRows != 5 || status.TerminalFingerprintRows != 5 {
		t.Fatalf("terminal rows keyword=%d fingerprint=%d, want 5/5", status.TerminalKeywordRows, status.TerminalFingerprintRows)
	}
	if status.TerminalKeywordLogicalBytes == 0 || status.TerminalFingerprintLogicalBytes == 0 {
		t.Fatal("terminal logical bytes are zero")
	}
	if len(status.TerminalGenerationEvidence) != 2 {
		t.Fatalf("evidence=%d, want 2", len(status.TerminalGenerationEvidence))
	}
	if status.TerminalGenerationEvidence[0].FailureClass == "" || status.TerminalGenerationEvidence[0].TerminalAt == "" {
		t.Fatalf("evidence missing failure_class/terminal_at: %+v", status.TerminalGenerationEvidence[0])
	}
}
