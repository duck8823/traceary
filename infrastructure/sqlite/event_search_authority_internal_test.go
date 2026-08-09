package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

// seedTieredCompleteProjection freezes a complete generation at the current
// source high-water and switches the store to tiered authority. Events saved
// after this call form the post-cutover tail.
func seedTieredCompleteProjection(t *testing.T, database *Database, generation string) {
	t.Helper()
	ctx := context.Background()
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	// Bind each statement separately: multi-statement Exec with shared
	// placeholders is unreliable for generation_id across the two state tables.
	if _, err = raw.ExecContext(ctx, `
		UPDATE literal_search_projection_state
		   SET generation_id = ?,
		       high_water = (SELECT COALESCE(MAX(sequence), 0) FROM search_projection_source_sequence),
		       state = 'complete'
		 WHERE singleton = 1`, generation); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `
		UPDATE search_projection_state
		   SET generation_id = ?,
		       active_generation_id = ?,
		       high_water = (SELECT COALESCE(MAX(sequence), 0) FROM search_projection_source_sequence),
		       state = 'complete',
		       phase = 'complete'
		 WHERE singleton = 1`, generation, generation); err != nil {
		t.Fatal(err)
	}
}

// seedLiteralFingerprints inserts the real CharacterizeLiteralQuery shape for a
// pre-cutover event: generation_id + source_sequence + event_id + 16-byte
// fingerprint blobs at fingerprint_version=1. Matching the rebuild writer in
// search_projection_rebuild.go so the SQL pre-filter is exercised.
func seedLiteralFingerprints(t *testing.T, database *Database, generation, eventID, body string) {
	t.Helper()
	ctx := context.Background()
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for _, fp := range apptypes.CharacterizeLiteralQuery(body).Fingerprints() {
		if _, err = raw.ExecContext(ctx, `
			INSERT INTO literal_search_fingerprints(
				generation_id, source_sequence, event_id, fingerprint, fingerprint_version
			)
			SELECT ?, sequence, ?, ?, 1
			  FROM search_projection_source_sequence
			 WHERE event_id = ?`,
			generation, eventID, []byte(fp), eventID,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func insertTieredSearchEvent(t *testing.T, database *Database, id, body string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err = raw.ExecContext(ctx, `
		INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at)
		VALUES (?, 'note', 'codex', 'codex', 's', 'w', ?, ?)`,
		id, body, at.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
}

func insertTieredCommandAudit(t *testing.T, database *Database, eventID, commandText string) {
	t.Helper()
	ctx := context.Background()
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err = raw.ExecContext(ctx, `
		INSERT INTO command_audits(
			event_id, command_text, input_text, output_text,
			input_truncated, output_truncated, exit_code, failed,
			input_original_bytes, output_original_bytes,
			command_wrapper, command_name, failure_reason
		) VALUES (?, ?, '', '', 0, 0, 0, 0, 0, 0, 'direct', 'echo', 'none')`,
		eventID, commandText,
	); err != nil {
		t.Fatal(err)
	}
}

func readProjectionStates(t *testing.T, database *Database) (literalState, boundedState string) {
	t.Helper()
	ctx := context.Background()
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if err = raw.QueryRowContext(ctx, `SELECT state FROM literal_search_projection_state WHERE singleton=1`).Scan(&literalState); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRowContext(ctx, `SELECT state FROM search_projection_state WHERE singleton=1`).Scan(&boundedState); err != nil {
		t.Fatal(err)
	}
	return literalState, boundedState
}

func metadataIDs(rows []apptypes.EventMetadata) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.EventID().String())
	}
	return ids
}

func eventIDs(events []*model.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID().String())
	}
	return ids
}

func newTieredAuthorityFixture(t *testing.T) (*Database, *EventDatasource) {
	t.Helper()
	database := NewDatabase(
		filepath.Join(t.TempDir(), "store.db"),
		os.DirFS(filepath.Join("..", "..", "schema", "sqlite", "migrations")),
	)
	if err := NewStoreManagementDatasource(database).Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return database, NewEventDatasource(database)
}

func TestTieredAuthoritySearchIncludesPostCutoverTail(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	insertTieredSearchEvent(t, database, "pre-match", "shared needle alpha", base)
	insertTieredSearchEvent(t, database, "pre-noise", "unrelated body", base.Add(time.Second))
	seedTieredCompleteProjection(t, database, "gen-tail")
	// Real generation fingerprints for the pre-cutover match so historical
	// rows are not universally fail-open. See TestTieredAuthorityFingerprintPreFilter
	// for exclusion of non-matching fingerprints.
	seedLiteralFingerprints(t, database, "gen-tail", "pre-match", "shared needle alpha")
	seedLiteralFingerprints(t, database, "gen-tail", "pre-noise", "unrelated body")

	// One ordinary post-cutover event widens sourceHigh past literal high_water
	// and flips literal state to stale via migration-039 triggers.
	insertTieredSearchEvent(t, database, "post-match", "shared needle beta", base.Add(2*time.Second))
	insertTieredSearchEvent(t, database, "post-noise", "still unrelated", base.Add(3*time.Second))

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	wantIDs := []string{"post-match", "pre-match"}

	t.Run("metadata path returns post-cutover matches", func(t *testing.T) {
		got, err := datasource.SearchMetadata(ctx, criteria)
		if err != nil {
			t.Fatalf("SearchMetadata() error = %v", err)
		}
		if diff := cmp.Diff(wantIDs, metadataIDs(got)); diff != "" {
			t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("full-event path returns post-cutover matches", func(t *testing.T) {
		got, err := datasource.SearchPage(ctx, criteria)
		if err != nil {
			t.Fatalf("SearchPage() error = %v", err)
		}
		if diff := cmp.Diff(wantIDs, eventIDs(got)); diff != "" {
			t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("public order is created_at_norm DESC, id DESC across the cutover", func(t *testing.T) {
		// Same timestamp, higher id must sort first among post/pre pairs.
		insertTieredSearchEvent(t, database, "post-z", "shared needle zeta", base.Add(4*time.Second))
		insertTieredSearchEvent(t, database, "post-a", "shared needle alpha-post", base.Add(4*time.Second))
		got, err := datasource.SearchPage(ctx, criteria)
		if err != nil {
			t.Fatalf("SearchPage() error = %v", err)
		}
		// post-z and post-a share created_at; id DESC puts post-z first.
		wantOrdered := []string{"post-z", "post-a", "post-match", "pre-match"}
		if diff := cmp.Diff(wantOrdered, eventIDs(got)); diff != "" {
			t.Fatalf("ordered IDs mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestTieredAuthorityFingerprintPreFilter(t *testing.T) {
	// Fingerprints are the real CharacterizeLiteralQuery 16-byte SHA-256
	// trigram digests at version 1, keyed by generation_id + event_id +
	// source_sequence — identical to the rebuild writer.
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "pre-match", "shared needle alpha", base)
	// Body would match "needle" if decoded, but fingerprints come from a
	// non-overlapping string so the pre-filter must exclude the row.
	insertTieredSearchEvent(t, database, "pre-excluded", "shared needle decoy", base.Add(time.Second))
	insertTieredSearchEvent(t, database, "pre-noise", "zzzz other text", base.Add(2*time.Second))
	const generation = "gen-fp"
	seedTieredCompleteProjection(t, database, generation)
	seedLiteralFingerprints(t, database, generation, "pre-match", "shared needle alpha")
	seedLiteralFingerprints(t, database, generation, "pre-excluded", "zzzz other text")
	seedLiteralFingerprints(t, database, generation, "pre-noise", "zzzz other text")

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff([]string{"pre-match"}, metadataIDs(got)); diff != "" {
		t.Fatalf("fingerprint pre-filter IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff([]string{"pre-match"}, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() fingerprint IDs mismatch (-want +got):\n%s", diff)
	}
}

// TestTieredAuthoritySearchAnswersWhenProjectionUnusable asserts that states
// which previously refused with "tiered search projection is incomplete or
// stale" now fail open (no fingerprint pre-filter) and still return correct
// decode-based matches. Fingerprints are an optimisation, not availability.
func TestTieredAuthoritySearchAnswersWhenProjectionUnusable(t *testing.T) {
	ctx := context.Background()
	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	want := []string{"e1"}
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		setup func(t *testing.T, database *Database)
	}{
		{
			name: "literal projection rebuilding",
			setup: func(t *testing.T, database *Database) {
				insertTieredSearchEvent(t, database, "e1", "needle", base)
				seedTieredCompleteProjection(t, database, "gen-a")
				raw, err := database.open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = raw.Close() }()
				if _, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET state='rebuilding' WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bounded projection rebuilding",
			setup: func(t *testing.T, database *Database) {
				insertTieredSearchEvent(t, database, "e1", "needle", base)
				seedTieredCompleteProjection(t, database, "gen-a")
				raw, err := database.open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = raw.Close() }()
				if _, err = raw.ExecContext(ctx, `UPDATE search_projection_state SET state='rebuilding' WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "generation mismatch",
			setup: func(t *testing.T, database *Database) {
				insertTieredSearchEvent(t, database, "e1", "needle", base)
				seedTieredCompleteProjection(t, database, "gen-a")
				raw, err := database.open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = raw.Close() }()
				if _, err = raw.ExecContext(ctx, `
					UPDATE literal_search_projection_state SET generation_id='gen-literal' WHERE singleton=1;
					UPDATE search_projection_state SET generation_id='gen-bounded', active_generation_id='gen-bounded' WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty literal generation",
			setup: func(t *testing.T, database *Database) {
				insertTieredSearchEvent(t, database, "e1", "needle", base)
				seedTieredCompleteProjection(t, database, "gen-a")
				raw, err := database.open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = raw.Close() }()
				if _, err = raw.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id='' WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			database, datasource := newTieredAuthorityFixture(t)
			tc.setup(t, database)

			got, err := datasource.SearchMetadata(ctx, criteria)
			if err != nil {
				t.Fatalf("SearchMetadata() error = %v, want matches without pre-filter", err)
			}
			if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
				t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
			}

			full, err := datasource.SearchPage(ctx, criteria)
			if err != nil {
				t.Fatalf("SearchPage() error = %v, want matches without pre-filter", err)
			}
			if diff := cmp.Diff(want, eventIDs(full)); diff != "" {
				t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTieredAuthoritySearchWithoutProjectionGeneration covers the fresh-store
// case #1718 creates: tiered authority with no generation ever built.
// TestTieredAuthoritySearchFindsEventsNeverInventoried covers the upgraded
// store: events that predate migration 038 have no search_projection_source_
// sequence row until the rebuild's inventory phase creates one
// (search_projection_rebuild.go:271). A candidate walk anchored on that table
// would silently omit the entire pre-upgrade history now that an unusable
// projection answers instead of refusing.
func TestTieredAuthoritySearchFindsEventsNeverInventoried(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "inventoried", "history needle new", base.Add(time.Second))
	insertTieredSearchEvent(t, database, "never-inventoried", "history needle old", base)

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the upgraded-store shape: the event exists but was never
	// registered, exactly as if it predated the migration-038 insert trigger.
	if _, err = raw.ExecContext(ctx, `DELETE FROM search_projection_source_sequence WHERE event_id='never-inventoried'`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	want := []string{"inventoried", "never-inventoried"}

	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff(want, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
}

// TestTieredAuthoritySearchIgnoresOrphanedSourceSequenceRows covers the other
// direction: nothing removes a source-sequence row when its event is deleted,
// so the table outlives the events it names.
func TestTieredAuthoritySearchIgnoresOrphanedSourceSequenceRows(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "kept", "orphan needle", base)
	insertTieredSearchEvent(t, database, "removed", "orphan needle gone", base.Add(time.Second))
	seedTieredCompleteProjection(t, database, "gen-orphan")

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `DELETE FROM events WHERE id='removed'`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	var orphans int
	if err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_projection_source_sequence WHERE event_id='removed'`).Scan(&orphans); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()
	if orphans != 1 {
		t.Fatalf("orphaned source-sequence rows = %d, want 1 (fixture no longer reproduces the case)", orphans)
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff([]string{"kept"}, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestTieredAuthoritySearchWithoutProjectionGeneration(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "older-match", "fresh needle alpha", base)
	insertTieredSearchEvent(t, database, "noise", "unrelated body", base.Add(time.Second))
	insertTieredSearchEvent(t, database, "newer-match", "fresh needle beta", base.Add(2*time.Second))

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	want := []string{"newer-match", "older-match"}

	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff(want, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
}

// TestTieredAuthoritySearchDuringRebuildSkipsOldPreFilter proves that after
// Start points literal at a new generation while bounded still names the
// previous one, search fails open rather than applying the old generation's
// fingerprints (which would silently false-negative).
func TestTieredAuthoritySearchDuringRebuildSkipsOldPreFilter(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// Body matches "needle", but old-generation fingerprints are from a
	// non-overlapping string so a usable pre-filter would exclude the row.
	insertTieredSearchEvent(t, database, "would-exclude", "shared needle decoy", base)
	insertTieredSearchEvent(t, database, "pre-match", "shared needle alpha", base.Add(time.Second))
	const oldGeneration = "gen-rebuild-old"
	seedTieredCompleteProjection(t, database, oldGeneration)
	seedLiteralFingerprints(t, database, oldGeneration, "would-exclude", "zzzz other text")
	seedLiteralFingerprints(t, database, oldGeneration, "pre-match", "shared needle alpha")

	// With a usable generation the decoy fingerprints exclude would-exclude.
	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	before, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() before Start error = %v", err)
	}
	if diff := cmp.Diff([]string{"pre-match"}, metadataIDs(before)); diff != "" {
		t.Fatalf("usable pre-filter IDs mismatch (-want +got):\n%s", diff)
	}

	budget := projectionBudget()
	if _, err = database.Start(ctx, budget, base.Add(time.Hour)); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// After Start: literalGeneration != boundedGeneration → no pre-filter.
	// would-exclude must now surface because its body really matches.
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() during rebuild error = %v", err)
	}
	if diff := cmp.Diff([]string{"pre-match", "would-exclude"}, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() during rebuild IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() during rebuild error = %v", err)
	}
	if diff := cmp.Diff([]string{"pre-match", "would-exclude"}, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() during rebuild IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestTieredAuthoritySearchAnswersWhenGenerationFailed(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "match", "failed needle", base)
	seedTieredCompleteProjection(t, database, "gen-failed")
	seedLiteralFingerprints(t, database, "gen-failed", "match", "failed needle")

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `
		UPDATE search_projection_state
		   SET state = 'failed', phase = 'complete', failure_class = 'test'
		 WHERE singleton = 1`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	want := []string{"match"}
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff(want, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
}

// TestTieredAuthoritySearchBudgetExhaustionWithoutPreFilter confirms that
// decode-bound walks without a fingerprint generation still surface budget
// exhaustion as EventSearchUnavailableIndexIncomplete rather than truncating.
func TestTieredAuthoritySearchBudgetExhaustionWithoutPreFilter(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "pre-match", "budget needle", base)
	// No complete generation: fingerprint pre-filter is skipped entirely.
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inflatedStored := apptypes.DeepLiteralSearchBudget.StoredBytes + 1
	if _, err = raw.ExecContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_encoded_bytes, body_plaintext_bytes
		) VALUES (
			'post-heavy', 'note', 'codex', 'codex', 's', 'w', 'no match here', ?,
			?, ?
		)`,
		base.Add(time.Minute).UTC().Format(time.RFC3339Nano),
		inflatedStored,
		inflatedStored,
	); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()

	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build()
	_, err = datasource.SearchMetadata(ctx, criteria)
	var unavailable *queryservice.EventSearchUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("SearchMetadata() error = %v, want EventSearchUnavailableError", err)
	}
	if unavailable.Reason != queryservice.EventSearchUnavailableIndexIncomplete {
		t.Fatalf("unavailable reason = %q, want %q", unavailable.Reason, queryservice.EventSearchUnavailableIndexIncomplete)
	}
	if unavailable.CandidateLimit != apptypes.DeepLiteralSearchBudget.SourceRows {
		t.Fatalf("CandidateLimit = %d, want %d", unavailable.CandidateLimit, apptypes.DeepLiteralSearchBudget.SourceRows)
	}

	_, err = datasource.SearchPage(ctx, criteria)
	if !errors.As(err, &unavailable) || unavailable.Reason != queryservice.EventSearchUnavailableIndexIncomplete {
		t.Fatalf("SearchPage() error = %v, want index_incomplete", err)
	}
}

func TestTieredAuthoritySearchBudgetExhaustionOnLargeTail(t *testing.T) {
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	// Pre-cutover match would answer the query if the walk ever reached it.
	insertTieredSearchEvent(t, database, "pre-match", "budget needle", base)
	seedTieredCompleteProjection(t, database, "gen-budget")

	// Post-cutover rows are newest, so they are visited first and consume the
	// DeepLiteralSearchBudget before the pre-cutover match is examined.
	// Inflated encoded/plaintext sizes trip the byte budget without storing
	// multi-megabyte bodies in the fixture.
	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// StoredBytes budget is 64MiB; one inflated post-cutover row exceeds it.
	inflatedStored := apptypes.DeepLiteralSearchBudget.StoredBytes + 1
	if _, err = raw.ExecContext(ctx, `
		INSERT INTO events(
			id, kind, client, agent, session_id, workspace, body, created_at,
			body_encoded_bytes, body_plaintext_bytes
		) VALUES (
			'post-heavy', 'note', 'codex', 'codex', 's', 'w', 'no match here', ?,
			?, ?
		)`,
		base.Add(time.Minute).UTC().Format(time.RFC3339Nano),
		inflatedStored,
		inflatedStored,
	); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()

	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build()
	_, err = datasource.SearchMetadata(ctx, criteria)
	var unavailable *queryservice.EventSearchUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("SearchMetadata() error = %v, want EventSearchUnavailableError", err)
	}
	if unavailable.Reason != queryservice.EventSearchUnavailableIndexIncomplete {
		t.Fatalf("unavailable reason = %q, want %q", unavailable.Reason, queryservice.EventSearchUnavailableIndexIncomplete)
	}
	if unavailable.CandidateLimit != apptypes.DeepLiteralSearchBudget.SourceRows {
		t.Fatalf("CandidateLimit = %d, want %d", unavailable.CandidateLimit, apptypes.DeepLiteralSearchBudget.SourceRows)
	}

	_, err = datasource.SearchPage(ctx, criteria)
	if !errors.As(err, &unavailable) || unavailable.Reason != queryservice.EventSearchUnavailableIndexIncomplete {
		t.Fatalf("SearchPage() error = %v, want index_incomplete", err)
	}
}

func TestTieredAuthoritySearchWatermarkGapIsNotStale(t *testing.T) {
	// Focused regression for the equality gate itself: generation is complete
	// and consistent, only sourceHigh has advanced past literal high_water.
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	insertTieredSearchEvent(t, database, "pre-only", "gap needle", base)
	seedTieredCompleteProjection(t, database, "gen-gap")

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var literalHigh, sourceHigh int64
	if err = raw.QueryRowContext(ctx, `SELECT high_water FROM literal_search_projection_state WHERE singleton=1`).Scan(&literalHigh); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	// Insert without matching body so the only success condition is "no stale error".
	if _, err = raw.ExecContext(ctx, `
		INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at)
		VALUES ('post-gap', 'note', 'codex', 'codex', 's', 'w', 'ordinary hook fire', ?)`,
		base.Add(time.Second).UTC().Format(time.RFC3339Nano),
	); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err = raw.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&sourceHigh); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()
	if sourceHigh <= literalHigh {
		t.Fatalf("fixture did not create a watermark gap: sourceHigh=%d literalHigh=%d", sourceHigh, literalHigh)
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v (watermark gap must not be treated as stale)", err)
	}
	if diff := cmp.Diff([]string{"pre-only"}, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}

	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff([]string{"pre-only"}, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestTieredAuthorityAppendLeavesLiteralStaleAndStillAnswers(t *testing.T) {
	// Case 1: complete generation, ordinary event append → literal stale,
	// bounded complete → both search paths return matches including the tail.
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "pre-match", "append needle alpha", base)
	seedTieredCompleteProjection(t, database, "gen-append")
	seedLiteralFingerprints(t, database, "gen-append", "pre-match", "append needle alpha")
	insertTieredSearchEvent(t, database, "post-match", "append needle beta", base.Add(time.Second))

	literalState, boundedState := readProjectionStates(t, database)
	if literalState != "stale" {
		t.Fatalf("literal state = %q, want stale after append", literalState)
	}
	if boundedState != "complete" {
		t.Fatalf("bounded state = %q, want complete after append", boundedState)
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	want := []string{"post-match", "pre-match"}

	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff(want, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestTieredAuthorityBodyUpdateOfProjectedEventDriftsAndAnswersMutated(t *testing.T) {
	// Case 2: body update of an already-projected event → bounded drifted with
	// active_generation_id=NULL → search still answers the *mutated* body by
	// decoding (pre-filter skipped). Assert bounded became drifted so a silent
	// trigger regression fails loudly rather than masking wrong answers.
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// Original body does not contain "mutated-unique"; fingerprints would
	// only cover the original. After mutation the decode path must find it.
	insertTieredSearchEvent(t, database, "projected", "update needle original", base)
	seedTieredCompleteProjection(t, database, "gen-update")
	seedLiteralFingerprints(t, database, "gen-update", "projected", "update needle original")

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE events SET body='mutated-unique content for drift' WHERE id='projected'`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	var activeGeneration sql.NullString
	if err = raw.QueryRowContext(ctx, `SELECT active_generation_id FROM search_projection_state WHERE singleton=1`).Scan(&activeGeneration); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()
	if activeGeneration.Valid && activeGeneration.String != "" {
		t.Fatalf("active_generation_id = %q, want NULL after drift trigger", activeGeneration.String)
	}

	literalState, boundedState := readProjectionStates(t, database)
	if boundedState != "drifted" {
		t.Fatalf("bounded state = %q, want drifted after projected body update (trigger regression?)", boundedState)
	}
	if literalState != "stale" {
		t.Fatalf("literal state = %q, want stale after body update", literalState)
	}

	// Query unique to the mutated body so an answer proves decode, not the
	// pre-mutation fingerprint index.
	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("mutated-unique").Build()
	want := []string{"projected"}
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v, want mutated content match", err)
	}
	if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v, want mutated content match", err)
	}
	if diff := cmp.Diff(want, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
	if full[0].Body() != "mutated-unique content for drift" {
		t.Fatalf("SearchPage() body = %q, want mutated content", full[0].Body())
	}
}

func TestTieredAuthorityAuditInsertOnProjectedEventDriftsAndAnswers(t *testing.T) {
	// Case 3: command_audits insert against an already-projected event
	// (sequence <= high_water) → bounded drifted → search still finds the
	// audit text by decoding without a fingerprint pre-filter.
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "projected", "audit projected body", base)
	seedTieredCompleteProjection(t, database, "gen-audit-pre")
	insertTieredCommandAudit(t, database, "projected", "needle in command")

	_, boundedState := readProjectionStates(t, database)
	if boundedState != "drifted" {
		t.Fatalf("bounded state = %q, want drifted after audit on projected event", boundedState)
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	want := []string{"projected"}
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v, want audit match after drift", err)
	}
	if diff := cmp.Diff(want, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestTieredAuthorityAuditInsertOnTailEventStaysCompleteAndMatches(t *testing.T) {
	// Case 4: command_audits insert against a tail event (sequence > high_water)
	// → bounded stays complete, no fingerprints → audit text is decoded and matches.
	ctx := context.Background()
	database, datasource := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	insertTieredSearchEvent(t, database, "pre-only", "no match here", base)
	seedTieredCompleteProjection(t, database, "gen-audit-tail")
	insertTieredSearchEvent(t, database, "tail-event", "tail body without needle", base.Add(time.Second))
	insertTieredCommandAudit(t, database, "tail-event", "needle lives in the audit command")

	literalState, boundedState := readProjectionStates(t, database)
	if boundedState != "complete" {
		t.Fatalf("bounded state = %q, want complete after audit on tail event", boundedState)
	}
	if literalState != "stale" {
		t.Fatalf("literal state = %q, want stale after audit insert", literalState)
	}

	criteria := apptypes.NewEventSearchCriteriaBuilder(10).Query("needle").Build()
	got, err := datasource.SearchMetadata(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchMetadata() error = %v", err)
	}
	if diff := cmp.Diff([]string{"tail-event"}, metadataIDs(got)); diff != "" {
		t.Fatalf("SearchMetadata() IDs mismatch (-want +got):\n%s", diff)
	}
	full, err := datasource.SearchPage(ctx, criteria)
	if err != nil {
		t.Fatalf("SearchPage() error = %v", err)
	}
	if diff := cmp.Diff([]string{"tail-event"}, eventIDs(full)); diff != "" {
		t.Fatalf("SearchPage() IDs mismatch (-want +got):\n%s", diff)
	}
}

func TestTieredAuthorityLiteralCompletionAcceptsStaleFromRebuildAppend(t *testing.T) {
	// Case 5: event appended during a rebuild, then generation completes →
	// literal transitions stale → complete and both projections end complete.
	ctx := context.Background()
	database, _ := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	insertTieredSearchEvent(t, database, "during-pre", "rebuild needle", base)

	budget := projectionBudget()
	budget.Rows = 8
	now := base.Add(time.Hour)
	if _, err := database.Start(ctx, budget, now); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	literalState, _ := readProjectionStates(t, database)
	if literalState != "rebuilding" {
		t.Fatalf("literal state after Start = %q, want rebuilding", literalState)
	}

	// Append during rebuild: migration-039 flips literal to stale.
	insertTieredSearchEvent(t, database, "during-append", "appended while rebuilding", base.Add(time.Second))
	literalState, _ = readProjectionStates(t, database)
	if literalState != "stale" {
		t.Fatalf("literal state after mid-rebuild append = %q, want stale", literalState)
	}

	uc := usecase.NewSearchProjectionUsecase(database)
	var completed bool
	for range 32 {
		progress, err := uc.Resume(ctx, budget, now)
		if err != nil {
			t.Fatalf("Resume() error = %v", err)
		}
		if progress.Completed {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("generation did not complete within batch budget")
	}

	literalState, boundedState := readProjectionStates(t, database)
	if literalState != "complete" {
		t.Fatalf("literal state after completion = %q, want complete (stale→complete transition)", literalState)
	}
	if boundedState != "complete" {
		t.Fatalf("bounded state after completion = %q, want complete", boundedState)
	}
}

func TestTieredAuthorityLiteralCompletionZeroRowsIsDrift(t *testing.T) {
	// Case 6: literal completion UPDATE matching zero rows → batch fails with
	// SearchProjectionDriftError rather than reporting success.
	ctx := context.Background()
	database, _ := newTieredAuthorityFixture(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	insertTieredSearchEvent(t, database, "e1", "completion needle", base)

	budget := projectionBudget()
	now := base.Add(time.Hour)
	g, err := database.Start(ctx, budget, now)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	raw, err := database.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Point the singleton at a different generation so the completion UPDATE
	// matches zero rows while bounded/lifecycle still look rebuildable.
	if _, err = raw.ExecContext(ctx, `
		UPDATE literal_search_projection_state
		   SET generation_id = 'not-this-generation', state = 'rebuilding'
		 WHERE singleton = 1`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	var rev, checkpoint int64
	var phase string
	if err = raw.QueryRowContext(ctx, `
		SELECT source_revision, checkpoint, phase
		  FROM search_projection_state
		 WHERE generation_id = ?`, g.GenerationID,
	).Scan(&rev, &checkpoint, &phase); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	_ = raw.Close()

	plan := apptypes.ProjectionBatchPlan{
		GenerationID:       g.GenerationID,
		Phase:              phase,
		ExpectedRevision:   rev,
		ExpectedCheckpoint: checkpoint,
		NextCheckpoint:     checkpoint,
		NextPhase:          "complete",
		Completed:          true,
		FinalState:         "complete",
		AllowRevisionDrift: true,
	}
	_, err = database.ApplyBatch(ctx, plan, budget.LockTime, now)
	var drift *apptypes.SearchProjectionDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("ApplyBatch() error = %T %v, want SearchProjectionDriftError", err, err)
	}
}
