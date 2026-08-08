package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
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
		       cutover_family_bytes_after=0,cutover_before_evidence_status='',cutover_before_evidence_reason='',cutover_after_evidence_status='',cutover_after_evidence_reason=''
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
// (measured at 1.44s for an ~880MiB family), so it cannot share the completion
// transaction's lock budget: a walk that overran it rolled the completion back
// and left every later open repeating the same final batch. When the
// measurement cannot produce a figure the generation still completes and the
// evidence says so, rather than recording a zero that reads exactly like a
// genuinely empty family.
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
			// Before and after carry their own evidence: they are measured at
			// different times against families of different sizes, and one
			// succeeding says nothing about the other.
			for label, got := range map[string]struct {
				evidence apptypes.CapacityEvidence
				bytes    int64
			}{
				"before": {status.CutoverBeforeEvidence, status.CutoverFamilyBytesBefore},
				"after":  {status.CutoverAfterEvidence, status.CutoverFamilyBytesAfter},
			} {
				if diff := cmp.Diff(test.wantStatus, got.evidence.Status); diff != "" {
					t.Errorf("%s evidence status (-want +got):\n%s", label, diff)
				}
				if diff := cmp.Diff("dbstat", got.evidence.Method); diff != "" {
					t.Errorf("%s evidence method (-want +got):\n%s", label, diff)
				}
				if !test.wantMeasured {
					// The distinguishing property: bytes are zero, and the
					// evidence is what says the zero means "not measured".
					if got.bytes != 0 {
						t.Errorf("%s bytes = %d, want 0", label, got.bytes)
					}
					if strings.TrimSpace(got.evidence.Reason) == "" {
						t.Errorf("%s unavailable evidence carries no reason", label)
					}
				} else if got.evidence.Reason != "" {
					t.Errorf("%s measured evidence carries a reason: %q", label, got.evidence.Reason)
				}
			}
			// Only the after figure has a family to measure; before runs against
			// an empty one, so its byte count is not asserted as positive.
			if test.wantMeasured && status.CutoverFamilyBytesAfter <= 0 {
				t.Errorf("measured family bytes after = %d, want > 0", status.CutoverFamilyBytesAfter)
			}
		})
	}
}

// TestSearchProjectionCutoverEvidence_RecordsOnACancelledBatchContext pins that
// evidence is written on a context detached from the batch. The batch context is
// sized for one bounded unit of work and is routinely near expiry by the time a
// generation completes, so a write inheriting it would fail exactly when the
// walk was slow enough to matter — leaving an unrecorded figure sitting at zero
// while the status still claimed "measured" from the start-time walk.
func TestSearchProjectionCutoverEvidence_RecordsOnACancelledBatchContext(t *testing.T) {
	database, _ := newProjectionEvidenceStore(t)
	driveProjectionToCompletion(t, database)
	raw, err := database.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err = raw.ExecContext(context.Background(), `
		UPDATE search_projection_state
		   SET cutover_family_bytes_after=0,cutover_after_evidence_status='',cutover_after_evidence_reason=''
		 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	database.recordSearchProjectionCutoverEvidence(cancelled, raw, activeGenerationID(t, raw), time.Now().UTC())

	after, err := database.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("measured", after.CutoverAfterEvidence.Status); diff != "" {
		t.Errorf("after evidence status on a cancelled batch context (-want +got):\n%s", diff)
	}
	if after.CutoverFamilyBytesAfter <= 0 {
		t.Errorf("after bytes = %d, want > 0", after.CutoverFamilyBytesAfter)
	}
}

// TestSearchProjectionCutoverEvidence_DoesNotOverwriteAReplacementGeneration
// pins the generation fence. Another process can start a replacement between the
// completion commit and the evidence write: that moves generation_id on but
// leaves active_generation_id pointing at the completed one, so matching on the
// active pointer alone would stamp the old generation's after-evidence over the
// new generation's before-evidence.
func TestSearchProjectionCutoverEvidence_DoesNotOverwriteAReplacementGeneration(t *testing.T) {
	database, _ := newProjectionEvidenceStore(t)
	driveProjectionToCompletion(t, database)
	raw, err := database.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	completedGeneration := activeGenerationID(t, raw)
	// A replacement generation: generation_id moves on, active stays behind.
	if _, err = raw.ExecContext(context.Background(), `
		UPDATE search_projection_state
		   SET generation_id='gen-replacement',state='rebuilding',phase='source',
		       cutover_family_bytes_after=0,cutover_after_evidence_status='',cutover_after_evidence_reason=''
		 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(context.Background(), `
		INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water)
		VALUES('gen-replacement','rebuilding','',0,0)`); err != nil {
		t.Fatal(err)
	}

	database.recordSearchProjectionCutoverEvidence(context.Background(), raw, completedGeneration, time.Now().UTC())

	after, err := database.SearchProjectionStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.CutoverAfterEvidence.Status != "" || after.CutoverFamilyBytesAfter != 0 {
		t.Errorf("replacement generation evidence overwritten: status=%q bytes=%d",
			after.CutoverAfterEvidence.Status, after.CutoverFamilyBytesAfter)
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

func activeGenerationID(t *testing.T, raw *sql.DB) string {
	t.Helper()
	var generation string
	if err := raw.QueryRowContext(context.Background(), `SELECT COALESCE(active_generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation == "" {
		t.Fatal("no active generation to record evidence against")
	}
	return generation
}
