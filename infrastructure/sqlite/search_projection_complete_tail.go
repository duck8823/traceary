package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// completeTailCatchUpMaxBatches caps one store-open drain of post-cutover
// sequences. Default Rows=128 so 32 batches cover 4096 source rows — the same
// bound DeepLiteralSearchBudget uses for a search walk. A busy host tail of
// ~1.6k events then drains in one Initialize instead of 13 opens.
const completeTailCatchUpMaxBatches = 32

// CatchUpCompleteGenerationTail fingerprints source sequences above high_water
// on a complete generation and advances high_water. It does not un-complete
// the generation or start a rebuild (#2173).
func (d *Database) CatchUpCompleteGenerationTail(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionCatchUpResult, error) {
	out := apptypes.SearchProjectionCatchUpResult{State: "complete", Completed: true}
	if b.Rows <= 0 {
		return out, xerrors.Errorf("complete-generation tail catch-up row budget must be positive")
	}
	db, err := d.open(ctx)
	if err != nil {
		return out, xerrors.Errorf("open store for complete-generation tail catch-up: %w", err)
	}
	defer func() { _ = db.Close() }()

	var generationID string
	var highWater int64
	var state string
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(generation_id,''), high_water, state FROM search_projection_state WHERE singleton=1`).Scan(&generationID, &highWater, &state); err != nil {
		return out, xerrors.Errorf("read complete-generation tail high_water: %w", err)
	}
	out.GenerationID = generationID
	out.State = state
	out.Checkpoint = highWater
	if state != "complete" || generationID == "" {
		return out, nil
	}

	for batch := 0; batch < completeTailCatchUpMaxBatches; batch++ {
		if err = ctx.Err(); err != nil {
			return out, xerrors.Errorf("complete-generation tail catch-up canceled: %w", err)
		}
		selected, written, nextWater, err := catchUpCompleteGenerationTailBatch(ctx, db, generationID, highWater, b.Rows, now)
		if err != nil {
			return out, err
		}
		if selected == 0 {
			break
		}
		out.Batches++
		out.Selected += selected
		out.Written += written
		out.Checkpoint = nextWater
		highWater = nextWater
		if selected < b.Rows {
			break
		}
	}
	return out, nil
}

func catchUpCompleteGenerationTailBatch(ctx context.Context, db *sql.DB, generationID string, highWater int64, limit int, now time.Time) (selected, written int, nextWater int64, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, highWater, xerrors.Errorf("begin complete-generation tail catch-up: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT q.sequence, q.event_id, COALESCE(e.body_availability,''), e.id IS NULL
  FROM search_projection_source_sequence q
  LEFT JOIN events e ON e.id=q.event_id
 WHERE q.sequence > ?
 ORDER BY q.sequence
 LIMIT ?`, highWater, limit)
	if err != nil {
		return 0, 0, highWater, xerrors.Errorf("select complete-generation tail: %w", err)
	}

	type tailRow struct {
		sequence     int64
		eventID      string
		availability string
		deleted      bool
	}
	batch := make([]tailRow, 0, limit)
	for rows.Next() {
		var row tailRow
		if err = rows.Scan(&row.sequence, &row.eventID, &row.availability, &row.deleted); err != nil {
			_ = rows.Close()
			return 0, 0, highWater, xerrors.Errorf("scan complete-generation tail: %w", err)
		}
		batch = append(batch, row)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, highWater, xerrors.Errorf("iterate complete-generation tail: %w", err)
	}
	if err = rows.Close(); err != nil {
		return 0, 0, highWater, xerrors.Errorf("close complete-generation tail: %w", err)
	}
	if len(batch) == 0 {
		return 0, 0, highWater, nil
	}

	written = 0
	for _, row := range batch {
		if row.deleted {
			continue
		}
		text, loadErr := completeTailEventText(ctx, tx, row.eventID, row.availability)
		if loadErr != nil {
			return 0, 0, highWater, loadErr
		}
		for _, fingerprint := range apptypes.CharacterizeLiteralQuery(text).Fingerprints() {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version) VALUES(?,?,?,?,1)`, generationID, row.sequence, row.eventID, []byte(fingerprint)); err != nil {
				return 0, 0, highWater, xerrors.Errorf("insert complete-generation tail fingerprint: %w", err)
			}
			written++
		}
	}

	nextWater = batch[len(batch)-1].sequence
	stamp := formatTimestamp(now.UTC())
	result, err := tx.ExecContext(ctx, `UPDATE search_projection_state SET high_water=?, checkpoint=?, updated_at=? WHERE singleton=1 AND generation_id=? AND state='complete' AND high_water=?`, nextWater, nextWater, stamp, generationID, highWater)
	if err != nil {
		return 0, 0, highWater, xerrors.Errorf("advance complete-generation tail high_water: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, 0, highWater, xerrors.Errorf("count complete-generation tail high_water update: %w", err)
	}
	if n != 1 {
		return 0, 0, highWater, xerrors.Errorf("complete-generation tail high_water did not advance")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET high_water=? WHERE generation_id=? AND state='complete'`, nextWater, generationID); err != nil {
		return 0, 0, highWater, xerrors.Errorf("advance complete-generation lifecycle high_water: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE literal_search_projection_state SET high_water=?, updated_at=? WHERE singleton=1 AND generation_id=?`, nextWater, stamp, generationID); err != nil {
		return 0, 0, highWater, xerrors.Errorf("advance complete-generation literal high_water: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, highWater, xerrors.Errorf("commit complete-generation tail catch-up: %w", err)
	}
	return len(batch), written, nextWater, nil
}

func completeTailEventText(ctx context.Context, tx *sql.Tx, eventID, availability string) (string, error) {
	var body string
	if availability == "available" {
		plain, err := loadEventPlaintext(ctx, tx, eventID)
		if err != nil {
			return "", xerrors.Errorf("load complete-generation tail body: %w", err)
		}
		body, _ = visibleEventBody(string(plain), types.BodyAvailabilityAvailable)
	}
	cmd, err := hydrateAuditPayload(ctx, tx, eventID, "command")
	if err != nil {
		return "", xerrors.Errorf("load complete-generation tail command: %w", err)
	}
	in, err := hydrateAuditPayload(ctx, tx, eventID, "input")
	if err != nil {
		return "", xerrors.Errorf("load complete-generation tail input: %w", err)
	}
	output, err := hydrateAuditPayload(ctx, tx, eventID, "output")
	if err != nil {
		return "", xerrors.Errorf("load complete-generation tail output: %w", err)
	}
	parts := make([]string, 0, 4)
	for _, v := range []string{body, cmd.String, in.String, output.String} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n"), nil
}
