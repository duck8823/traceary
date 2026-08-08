package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// newProjectionEvidenceStore builds a migrated store holding one session and a
// couple of events, parked at idle so the test owns the generation.
func newProjectionEvidenceStore(t *testing.T) (*Database, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	database := NewDatabase(dbPath, os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")))
	if err := NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err = raw.ExecContext(ctx, `
		INSERT OR IGNORE INTO sessions(session_id,started_at,client,agent,workspace)
		VALUES('sess-evidence','2026-01-05T12:00:00Z','cli','codex','github.com/duck8823/traceary')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"evt-evidence-1", "evt-evidence-2"} {
		if _, err = raw.ExecContext(ctx, `
			INSERT INTO events(id,kind,client,agent,session_id,workspace,body,created_at)
			VALUES(?,'note','cli','codex','sess-evidence','github.com/duck8823/traceary','cutover evidence body','2026-01-05T12:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	// Initialize already ran one catch-up unit; park the projection so each
	// test drives the generation from a known idle state.
	if _, err = raw.ExecContext(ctx, `
		UPDATE search_projection_state
		   SET generation_id=NULL,active_generation_id=NULL,config_hash='',source_revision=0,
		       high_water=0,checkpoint=0,phase='source',cleanup_scope='old',failure_class='',
		       state='idle',cutover_index_family='',cutover_family_bytes_before=0,
		       cutover_family_bytes_after=0,cutover_evidence_status='',cutover_evidence_reason=''
		 WHERE singleton=1;
		DELETE FROM search_projection_generation_lifecycle;
		DELETE FROM search_projection_recent_documents;
		DELETE FROM search_projection_session_summaries;
		DELETE FROM search_projection_session_keywords;
		DELETE FROM search_projection_command_aggregates;
		DELETE FROM literal_search_fingerprints;
		UPDATE search_projection_inventory_state SET generation_id='',cursor='',cursor_started=0,state='idle' WHERE singleton=1;
		UPDATE literal_search_projection_state SET generation_id='',high_water=0,state='missing' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	return database, dbPath
}

// driveProjectionToCompletion runs bounded catch-up units the way repeated
// store opens would, and fails the test if the generation never completes.
func driveProjectionToCompletion(t *testing.T, database *Database) {
	t.Helper()
	ctx := context.Background()
	for attempt := 0; attempt < 50; attempt++ {
		result, err := catchUpSearchProjection(ctx, database)
		if err != nil {
			t.Fatalf("catch-up attempt %d: %v", attempt, err)
		}
		if result.Completed {
			return
		}
		if result.Action == "skipped" {
			t.Fatalf("catch-up attempt %d skipped: %s", attempt, result.SkippedReason)
		}
	}
	t.Fatal("projection never completed within 50 catch-up units")
}

// TestSearchProjectionCutoverEvidence_CompletionSurvivesUnmeasurableFamily pins
// the split between a completed generation and its diagnostic byte figures. The
// dbstat walk costs in proportion to the projection family's own page count
// (measured at 1.44s for an ~880MiB family), so it cannot be allowed to share
// the completion transaction's lock budget: a walk that overran it rolled the
// completion back and left every later open repeating the same final batch.
// When the measurement cannot produce a figure the generation still completes
// and the evidence says so, rather than recording a zero that reads exactly
// like a genuinely empty family.
func TestSearchProjectionCutoverEvidence_CompletionSurvivesUnmeasurableFamily(t *testing.T) {
	tests := map[string]struct {
		measureTimeout time.Duration
		wantStatus     string
		wantMeasured   bool
	}{
		"measurable family reports measured evidence":             {measureTimeout: 0, wantStatus: "measured", wantMeasured: true},
		"unmeasurable family reports unavailable, not zero bytes": {measureTimeout: time.Nanosecond, wantStatus: "unavailable"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			database, _ := newProjectionEvidenceStore(t)
			database.searchProjectionMeasureTimeoutOverride = test.measureTimeout
			driveProjectionToCompletion(t, database)

			status, err := database.SearchProjectionStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !status.Completed {
				t.Fatalf("generation state = %q, want complete", status.State)
			}
			if diff := cmp.Diff(test.wantStatus, status.CutoverFamilyEvidence.Status); diff != "" {
				t.Errorf("cutover evidence status (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("dbstat", status.CutoverFamilyEvidence.Method); diff != "" {
				t.Errorf("cutover evidence method (-want +got):\n%s", diff)
			}
			switch {
			case test.wantMeasured:
				if status.CutoverFamilyBytesAfter <= 0 {
					t.Errorf("measured family bytes = %d, want > 0", status.CutoverFamilyBytesAfter)
				}
				if status.CutoverFamilyEvidence.Reason != "" {
					t.Errorf("measured evidence carries a reason: %q", status.CutoverFamilyEvidence.Reason)
				}
			default:
				// The distinguishing property: bytes are zero, and the evidence
				// is what says the zero means "not measured".
				if status.CutoverFamilyBytesAfter != 0 {
					t.Errorf("unavailable family bytes = %d, want 0", status.CutoverFamilyBytesAfter)
				}
				if strings.TrimSpace(status.CutoverFamilyEvidence.Reason) == "" {
					t.Error("unavailable evidence carries no reason")
				}
			}
		})
	}
}

// TestSearchProjectionCatchUp_ParksDeterministicFailureInsteadOfRestarting pins
// that a failed generation is not restarted on every store open. Every failure
// class this store records is deterministic — an oversize row exceeds the same
// budget every time, session_tier_unverified fails the same query, abandoned is
// an operator decision — so auto-starting a replacement would fail identically
// and append a lifecycle row per open, forever.
func TestSearchProjectionCatchUp_ParksDeterministicFailureInsteadOfRestarting(t *testing.T) {
	ctx := context.Background()
	for _, failureClass := range []string{"decoded_bytes", "session_tier_unverified", "abandoned"} {
		t.Run(failureClass, func(t *testing.T) {
			database, _ := newProjectionEvidenceStore(t)
			raw, err := database.open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = raw.ExecContext(ctx, `
				UPDATE search_projection_state
				   SET generation_id='gen-failed',state='failed',phase='complete',failure_class=?
				 WHERE singleton=1`, failureClass); err != nil {
				t.Fatal(err)
			}
			if _, err = raw.ExecContext(ctx, `
				INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water)
				VALUES('gen-failed','rebuilding','',0,0)`); err != nil {
				t.Fatal(err)
			}
			_ = raw.Close()

			for open := 0; open < 3; open++ {
				result, catchUpErr := catchUpSearchProjection(ctx, database)
				if catchUpErr != nil {
					t.Fatalf("open %d: %v", open, catchUpErr)
				}
				if diff := cmp.Diff("skipped", result.Action); diff != "" {
					t.Fatalf("open %d action (-want +got):\n%s", open, diff)
				}
				if !strings.Contains(result.SkippedReason, failureClass) {
					t.Fatalf("open %d skip reason %q does not name the failure class", open, result.SkippedReason)
				}
			}
			if generations := lifecycleGenerationCount(t, database); generations != 1 {
				t.Errorf("lifecycle rows after three opens = %d, want 1 (no replacement generations)", generations)
			}
		})
	}
}

func lifecycleGenerationCount(t *testing.T, database *Database) int {
	t.Helper()
	raw, err := database.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var count int
	if err = raw.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM search_projection_generation_lifecycle`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
