package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
)

// defaultSearchProjectionCatchUpBudget is the automatic generation budget used
// on every store open. One Resume per open keeps the unit of work bounded;
// wall-time is deliberately short so Initialize never blocks on a multi-minute
// rebuild of a large store.
func defaultSearchProjectionCatchUpBudget() apptypes.SearchProjectionBudget {
	return apptypes.DefaultSearchProjectionBudget()
}

// catchUpSearchProjection advances the bounded projection by one durable unit
// of work during store initialization. Failures are returned to the caller so
// Initialize can log and continue — the same fail-open pattern as event search
// backfill.
//
// Failing open costs work, not correctness. It used to say the legacy index
// stayed authoritative until a generation completed; that stopped being true
// when #1718 retired the migration-032 family, and event_search_authority.go
// never consults it. Until a generation completes, the tiered path decides
// each candidate by decoding it, so results stay correct and a broad query on
// a large store reports index_incomplete instead of returning a partial page.
func catchUpSearchProjection(ctx context.Context, store *Database) (apptypes.SearchProjectionCatchUpResult, error) {
	db, err := store.open(ctx)
	if err != nil {
		return apptypes.SearchProjectionCatchUpResult{}, xerrors.Errorf("open store for projection catch-up probe: %w", err)
	}
	complete, missing, err := searchProjectionSchemaComplete(ctx, db)
	_ = db.Close()
	if err != nil {
		return apptypes.SearchProjectionCatchUpResult{}, err
	}
	if !complete {
		// Focused historical-schema tests build stores from hand-written
		// fixtures that omit migrations 038+. Decide that from the schema
		// itself rather than from the text of a failure: matching "no such
		// table" on an error would also swallow a genuine SQL defect in the
		// projection, and a swallowed defect means the generation silently
		// never completes.
		return apptypes.SearchProjectionCatchUpResult{
			Action:        "skipped",
			SkippedReason: "projection schema incomplete: " + missing,
		}, nil
	}
	result, err := usecase.NewSearchProjectionUsecase(store).CatchUp(ctx, defaultSearchProjectionCatchUpBudget(), time.Now().UTC())
	if err != nil {
		return result, xerrors.Errorf("search projection catch-up: %w", err)
	}
	return result, nil
}

// searchProjectionObjects are the tables the generation machinery reads or
// writes, created by migrations 038-042. A store either has all of them or is
// a fixture predating the bounded projection.
var searchProjectionObjects = []string{
	"literal_search_fingerprints",
	"literal_search_projection_state",
	"search_projection_command_aggregates",
	"search_projection_generation_lifecycle",
	"search_projection_exclusions",
	"search_projection_inventory_compat",
	"search_projection_inventory_state",
	"search_projection_recent_documents",
	"search_projection_recent_fts",
	"search_projection_session_keywords",
	"search_projection_session_summaries",
	"search_projection_source_revision",
	"search_projection_source_sequence",
	"search_projection_state",
}

// searchProjectionSchemaComplete reports whether every object the catch-up
// needs exists, naming the first missing one so a skip is diagnosable.
func searchProjectionSchemaComplete(ctx context.Context, db *sql.DB) (bool, string, error) {
	for _, object := range searchProjectionObjects {
		exists, err := sqliteTableExists(ctx, db, object)
		if err != nil {
			return false, "", err
		}
		if !exists {
			return false, object, nil
		}
	}
	// Additive columns land on an existing table, so presence of
	// search_projection_state alone does not imply a complete schema.
	// Check the newest required column: a store at 049-054 has
	// cutover_index_family but not index_family_byte_limit (#1679).
	var columns int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('search_projection_state') WHERE name IN ('index_family_byte_limit','recent_source_bytes')`,
	).Scan(&columns); err != nil {
		return false, "", xerrors.Errorf("inspect search_projection_state columns: %w", err)
	}
	if columns != 2 {
		return false, "search_projection_state.recent_source_bytes", nil
	}
	return true, "", nil
}

// SearchProjectionHasSourceWork reports whether any event exists that a new
// generation would need to inventory or project.
func (d *Database) SearchProjectionHasSourceWork(ctx context.Context) (bool, error) {
	db, err := d.open(ctx)
	if err != nil {
		return false, xerrors.Errorf("open store for projection source probe: %w", err)
	}
	defer func() { _ = db.Close() }()
	var hasEvents int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events LIMIT 1)`).Scan(&hasEvents); err != nil {
		return false, xerrors.Errorf("probe events for projection catch-up: %w", err)
	}
	if hasEvents != 0 {
		return true, nil
	}
	var requiresInventory int
	if err := db.QueryRowContext(ctx, `SELECT requires_inventory FROM search_projection_inventory_compat WHERE singleton=1`).Scan(&requiresInventory); err != nil {
		if isMissingSQLiteObject(err) {
			return false, nil
		}
		return false, xerrors.Errorf("probe inventory compat for projection catch-up: %w", err)
	}
	return requiresInventory != 0, nil
}

// logSearchProjectionCatchUp emits structured progress without failing Initialize.
func logSearchProjectionCatchUp(result apptypes.SearchProjectionCatchUpResult, err error) {
	if err != nil {
		var noProgress *apptypes.SearchProjectionNoProgressError
		if errors.As(err, &noProgress) && noProgress.Code == apptypes.SearchProjectionNoProgressSingleRowLockDurationCap {
			slog.Error("search projection catch-up cannot advance past checkpoint; re-running will not change this on its own; abort and start a new generation with a larger lock duration",
				"action", result.Action,
				"state", result.State,
				"phase", result.Phase,
				"checkpoint", result.Checkpoint,
				"generation_id", result.GenerationID,
				"error", err,
			)
			return
		}
		slog.Error("search projection catch-up incomplete; retrying on next initialization",
			"action", result.Action,
			"state", result.State,
			"phase", result.Phase,
			"generation_id", result.GenerationID,
			"batches", result.Batches,
			"selected", result.Selected,
			"written", result.Written,
			"error", err,
		)
		return
	}
	// Quiet terminal/no-op states stay silent. Permanent skips (especially a
	// budget mismatch left by an abandoned operator rebuild) must be visible:
	// the generation will never reach complete on its own.
	if result.Action == "already_complete" || result.Action == "none" {
		return
	}
	if result.Action == "skipped" {
		slog.Warn("search projection catch-up skipped; the generation will not advance on its own until the reason is addressed",
			"action", result.Action,
			"state", result.State,
			"phase", result.Phase,
			"generation_id", result.GenerationID,
			"reason", result.SkippedReason,
		)
		return
	}
	attrs := []any{
		"action", result.Action,
		"state", result.State,
		"phase", result.Phase,
		"generation_id", result.GenerationID,
		"batches", result.Batches,
		"selected", result.Selected,
		"written", result.Written,
		"completed", result.Completed,
		"cutover_index_family", result.CutoverIndexFamily,
		"cutover_family_bytes_before", result.CutoverFamilyBytesBefore,
		"cutover_family_bytes_after", result.CutoverFamilyBytesAfter,
		"session_tier_verified", result.SessionTierVerified,
	}
	if result.Completed {
		slog.Info("search projection catch-up completed generation", attrs...)
		return
	}
	slog.Debug("search projection catch-up batch completed", attrs...)
}
