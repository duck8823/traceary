package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// eventSearchBackfillBatchSize bounds the body-bearing work performed by one
// store initialization. Later initializations resume from the durable TEXT
// event-ID checkpoint; request-time search remains complete through its
// separately capped legacy fallback while this state is incomplete.
const eventSearchBackfillBatchSize = 128

type eventSearchBackfillResult struct {
	Selected    int
	Inserted    int64
	LastEventID string
	TargetID    string
	Completed   bool
}

func catchUpEventSearchDocuments(
	ctx context.Context,
	db *sql.DB,
	batchSize int,
) (eventSearchBackfillResult, error) {
	if batchSize <= 0 {
		return eventSearchBackfillResult{}, xerrors.Errorf("event search backfill batch size must be positive")
	}
	exists, err := sqliteTableExists(ctx, db, "event_search_backfill_state")
	if err != nil {
		return eventSearchBackfillResult{}, err
	}
	if !exists {
		// Focused historical-schema tests may omit migration 32.
		return eventSearchBackfillResult{Completed: true}, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return eventSearchBackfillResult{}, xerrors.Errorf("failed to begin event search backfill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		lastEventID string
		targetID    sql.NullString
		completed   int
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT last_event_id, target_event_id, completed
		  FROM event_search_backfill_state
		 WHERE singleton = 1`,
	).Scan(&lastEventID, &targetID, &completed); err != nil {
		return eventSearchBackfillResult{}, xerrors.Errorf("failed to read event search backfill state: %w", err)
	}
	result := eventSearchBackfillResult{
		LastEventID: lastEventID,
		Completed:   completed != 0,
	}
	if targetID.Valid {
		result.TargetID = targetID.String
	}
	if result.Completed {
		if err := tx.Commit(); err != nil {
			return result, xerrors.Errorf("failed to finish completed event search backfill check: %w", err)
		}
		return result, nil
	}

	if !targetID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), '') FROM events`).Scan(&result.TargetID); err != nil {
			return result, xerrors.Errorf("failed to freeze event search backfill target: %w", err)
		}
		if result.TargetID == "" {
			result.Completed = true
			if err := updateEventSearchBackfillState(ctx, tx, "", "", true); err != nil {
				return result, err
			}
			if err := tx.Commit(); err != nil {
				return result, xerrors.Errorf("failed to commit empty event search backfill: %w", err)
			}
			return result, nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE event_search_backfill_state
			   SET target_event_id = ?, updated_at = ?
			 WHERE singleton = 1`,
			result.TargetID,
			formatTimestamp(time.Now()),
		); err != nil {
			return result, xerrors.Errorf("failed to persist event search backfill target: %w", err)
		}
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		  FROM events
		 WHERE id > ? AND id <= ?
		 ORDER BY id
		 LIMIT ?`,
		lastEventID,
		result.TargetID,
		batchSize,
	)
	if err != nil {
		return result, xerrors.Errorf("failed to select event search backfill batch: %w", err)
	}
	selectedIDs := make([]string, 0, batchSize)
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			_ = rows.Close()
			return result, xerrors.Errorf("failed to scan event search backfill event ID: %w", err)
		}
		selectedIDs = append(selectedIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, xerrors.Errorf("failed to iterate event search backfill event IDs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return result, xerrors.Errorf("failed to close event search backfill event IDs: %w", err)
	}
	result.Selected = len(selectedIDs)

	if len(selectedIDs) == 0 {
		result.Completed = true
		if err := updateEventSearchBackfillState(ctx, tx, lastEventID, result.TargetID, true); err != nil {
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, xerrors.Errorf("failed to commit completed event search backfill: %w", err)
		}
		return result, nil
	}

	result.LastEventID = selectedIDs[len(selectedIDs)-1]
	for _, eventID := range selectedIDs {
		plain, err := loadEventPlaintext(ctx, tx, eventID)
		if err != nil {
			return result, xerrors.Errorf("decode event search backfill body: %w", err)
		}
		bodyText, _ := visibleEventBody(string(plain), types.BodyAvailabilityAvailable)
		command, err := hydrateAuditPayload(ctx, tx, eventID, "command")
		if err != nil {
			return result, err
		}
		input, err := hydrateAuditPayload(ctx, tx, eventID, "input")
		if err != nil {
			return result, err
		}
		output, err := hydrateAuditPayload(ctx, tx, eventID, "output")
		if err != nil {
			return result, err
		}
		insertResult, err := tx.ExecContext(ctx, `INSERT INTO event_search_documents(event_id, body_text, command_text, input_text, output_text)
VALUES (?, ?, ?, ?, ?) ON CONFLICT(event_id) DO NOTHING`, eventID, bodyText, command.String, input.String, output.String)
		if err != nil {
			return result, xerrors.Errorf("materialize event search backfill row: %w", err)
		}
		inserted, err := insertResult.RowsAffected()
		if err != nil {
			return result, xerrors.Errorf("count materialized event search row: %w", err)
		}
		result.Inserted += inserted
	}

	result.Completed = result.LastEventID >= result.TargetID
	if err := updateEventSearchBackfillState(
		ctx,
		tx,
		result.LastEventID,
		result.TargetID,
		result.Completed,
	); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, xerrors.Errorf("failed to commit event search backfill: %w", err)
	}
	return result, nil
}

func updateEventSearchBackfillState(
	ctx context.Context,
	tx *sql.Tx,
	lastEventID string,
	targetID string,
	completed bool,
) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE event_search_backfill_state
		   SET last_event_id = ?,
		       target_event_id = ?,
		       completed = ?,
		       updated_at = ?
		 WHERE singleton = 1`,
		lastEventID,
		targetID,
		boolToInt(completed),
		formatTimestamp(time.Now()),
	)
	if err != nil {
		return xerrors.Errorf("failed to update event search backfill state: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to count event search backfill state updates: %w", err)
	}
	if updated != 1 {
		return xerrors.Errorf("event search backfill state updated %d rows, want 1", updated)
	}
	return nil
}

func eventSearchBackfillComplete(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	var completed int
	err := queryer.QueryRowContext(ctx, `
		SELECT completed
		  FROM event_search_backfill_state
		 WHERE singleton = 1`,
	).Scan(&completed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, xerrors.Errorf("event search backfill state is missing")
		}
		return false, xerrors.Errorf("failed to inspect event search backfill state: %w", err)
	}
	return completed != 0, nil
}
