package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

// Sentinel errors for live-store payload backfill.
var (
	// ErrPayloadBackfillRecipeMismatch refuses resume across recipe changes.
	ErrPayloadBackfillRecipeMismatch = errors.New("payload backfill recipe version mismatch")
	// ErrPayloadBackfillNoResumableRun means resume found no running/paused run.
	ErrPayloadBackfillNoResumableRun = errors.New("no resumable payload backfill run")
	// ErrPayloadBackfillActiveRun means run was requested while another is open.
	ErrPayloadBackfillActiveRun = errors.New("a payload backfill run is already active")
	// ErrPayloadBackfillPartialMetadata fails the run closed on corrupt rows.
	ErrPayloadBackfillPartialMetadata = errors.New("payload backfill found partial codec metadata")
	// ErrPayloadBackfillTableMissing means migration 054 has not been applied.
	ErrPayloadBackfillTableMissing = errors.New("payload backfill bookkeeping table is missing")
)

// PayloadBackfillDatasource rewrites events.body in place through the codec.
type PayloadBackfillDatasource struct {
	db *Database
	// onBeforeCommitBatch is a test hook that fires after a batch is selected
	// and before the verify/encode transaction. Production leaves it nil.
	onBeforeCommitBatch func(batch []backfillCandidate)
}

// NewPayloadBackfillDatasource binds the live store path.
func NewPayloadBackfillDatasource(db *Database) *PayloadBackfillDatasource {
	return &PayloadBackfillDatasource{db: db}
}

var _ application.PayloadBackfill = (*PayloadBackfillDatasource)(nil)

type backfillRunRow struct {
	RunID               string
	RecipeVersion       string
	HighWaterRowID      int64
	CursorRowID         int64
	PassCount           int64
	State               apptypes.PayloadBackfillState
	StartedAt           string
	UpdatedAt           string
	CompletedAt         sql.NullString
	ScannedRows         int64
	EncodedRows         int64
	IdentityKeptRows    int64
	ConflictedRows      int64
	PartialMetadataRows int64
	RewrittenRows       int64
	PlaintextBytes      int64
	StoredBytes         int64
	FailureEventID      sql.NullString
	FailureReason       sql.NullString
}

type backfillCandidate struct {
	RowID     int64
	EventID   string
	Stored    []byte
	SourceSHA string
	Row       payloadRow
}

type backfillBatchStats struct {
	Scanned        int64
	Encoded        int64
	IdentityKept   int64
	Conflicted     int64
	Partial        int64
	Rewritten      int64
	PlaintextBytes int64
	StoredBytes    int64
	LastCursor     int64
	PartialEventID string
	PartialReason  string
}

// Preview reports eligible-row counts without mutating the store.
func (d *PayloadBackfillDatasource) Preview(ctx context.Context, c apptypes.PayloadBackfillConfig) (apptypes.PayloadBackfillResult, error) {
	if !c.Valid() {
		return apptypes.PayloadBackfillResult{}, xerrors.Errorf("invalid payload backfill config")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	defer d.db.release(db)

	if err = requirePayloadBackfillSchema(ctx, db); err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	highWater, err := maxEventRowID(ctx, db)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	eligible, err := countEligibleBackfillRows(ctx, db, 0, highWater)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	return apptypes.PayloadBackfillResult{
		State:          "planned",
		RecipeVersion:  apptypes.PayloadBackfillRecipeVersion,
		HighWaterRowID: highWater,
		EligibleRows:   eligible,
		MorePending:    eligible > 0,
	}, nil
}

// Status reports the latest run without advancing it.
func (d *PayloadBackfillDatasource) Status(ctx context.Context) (apptypes.PayloadBackfillResult, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	defer d.db.release(db)

	if err = requirePayloadBackfillSchema(ctx, db); err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	run, err := loadLatestBackfillRun(ctx, db)
	if errors.Is(err, sql.ErrNoRows) {
		return apptypes.PayloadBackfillResult{State: "idle", RecipeVersion: apptypes.PayloadBackfillRecipeVersion}, nil
	}
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	return resultFromRun(run, 0, false), nil
}

// Run starts a new in-place rewrite under a high-water fixed at start.
func (d *PayloadBackfillDatasource) Run(ctx context.Context, c apptypes.PayloadBackfillConfig) (apptypes.PayloadBackfillResult, error) {
	return d.execute(ctx, c, false)
}

// Resume continues a running or paused run under the same recipe version.
func (d *PayloadBackfillDatasource) Resume(ctx context.Context, c apptypes.PayloadBackfillConfig) (apptypes.PayloadBackfillResult, error) {
	return d.execute(ctx, c, true)
}

func (d *PayloadBackfillDatasource) execute(ctx context.Context, c apptypes.PayloadBackfillConfig, resume bool) (apptypes.PayloadBackfillResult, error) {
	if !c.Valid() {
		return apptypes.PayloadBackfillResult{}, xerrors.Errorf("invalid payload backfill config")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	defer d.db.release(db)

	if err = requirePayloadBackfillSchema(ctx, db); err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	run, err := prepareBackfillRun(ctx, db, resume)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	var batchCount int64
	for {
		if err = ctx.Err(); err != nil {
			if pauseErr := pauseBackfillRun(ctx, db, run.RunID); pauseErr != nil {
				return apptypes.PayloadBackfillResult{}, xerrors.Errorf("%v; pause payload backfill: %w", err, pauseErr)
			}
			loaded, loadErr := loadBackfillRun(ctx, db, run.RunID)
			if loadErr != nil {
				return apptypes.PayloadBackfillResult{}, loadErr
			}
			return resultFromRun(loaded, batchCount, true), xerrors.Errorf("payload backfill cancelled: %w", err)
		}

		// Begin a new pass when the cursor is at the origin.
		if run.CursorRowID == 0 {
			run.PassCount++
			run.passDirty = false
		}

		batch, selectErr := selectBackfillBatch(ctx, db, run.CursorRowID, run.HighWaterRowID, c.BatchRows)
		if selectErr != nil {
			return apptypes.PayloadBackfillResult{}, selectErr
		}
		if len(batch) == 0 {
			// End of a cursor walk. Full identity (incompressible) rows stay
			// selectable forever, so completion is a walk that rewrites nothing
			// and skips no conflicts — not "zero eligible rows".
			if !run.passDirty {
				if completeErr := completeBackfillRun(ctx, db, run.RunID); completeErr != nil {
					return apptypes.PayloadBackfillResult{}, completeErr
				}
				loaded, loadErr := loadBackfillRun(ctx, db, run.RunID)
				if loadErr != nil {
					return apptypes.PayloadBackfillResult{}, loadErr
				}
				return resultFromRun(loaded, batchCount, false), nil
			}
			// Conflicts or rewrites this pass: restart from rowid 0 under the
			// same high-water so skipped rows are not stranded.
			if resetErr := resetBackfillCursor(ctx, db, run.RunID); resetErr != nil {
				return apptypes.PayloadBackfillResult{}, resetErr
			}
			run.CursorRowID = 0
			continue
		}

		if d.onBeforeCommitBatch != nil {
			d.onBeforeCommitBatch(batch)
		}
		stats, commitErr := commitBackfillBatch(ctx, db, run, batch)
		if commitErr != nil {
			if errors.Is(commitErr, ErrPayloadBackfillPartialMetadata) {
				failed, failErr := failBackfillRun(ctx, db, run.RunID, stats.PartialEventID, stats.PartialReason, stats)
				if failErr != nil {
					return apptypes.PayloadBackfillResult{}, failErr
				}
				return resultFromRun(failed, batchCount, false), xerrors.Errorf("%w: event %s: %s", ErrPayloadBackfillPartialMetadata, stats.PartialEventID, stats.PartialReason)
			}
			return apptypes.PayloadBackfillResult{}, commitErr
		}
		batchCount++
		run.ScannedRows += stats.Scanned
		run.EncodedRows += stats.Encoded
		run.IdentityKeptRows += stats.IdentityKept
		run.ConflictedRows += stats.Conflicted
		run.PartialMetadataRows += stats.Partial
		run.RewrittenRows += stats.Rewritten
		run.PlaintextBytes += stats.PlaintextBytes
		run.StoredBytes += stats.StoredBytes
		run.CursorRowID = stats.LastCursor
		if stats.Rewritten > 0 || stats.Conflicted > 0 {
			run.passDirty = true
		}

		if c.StopAfterBatches > 0 && batchCount >= c.StopAfterBatches {
			if pauseErr := pauseBackfillRun(ctx, db, run.RunID); pauseErr != nil {
				return apptypes.PayloadBackfillResult{}, pauseErr
			}
			loaded, loadErr := loadBackfillRun(ctx, db, run.RunID)
			if loadErr != nil {
				return apptypes.PayloadBackfillResult{}, loadErr
			}
			return resultFromRun(loaded, batchCount, true), nil
		}
	}
}

// passDirty is tracked on the in-memory handle for the current pass only.
type backfillRunHandle struct {
	backfillRunRow
	passDirty bool
}

func prepareBackfillRun(ctx context.Context, db *sql.DB, resume bool) (backfillRunHandle, error) {
	active, err := loadActiveBackfillRun(ctx, db)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return backfillRunHandle{}, err
	}
	if resume {
		if errors.Is(err, sql.ErrNoRows) {
			return backfillRunHandle{}, ErrPayloadBackfillNoResumableRun
		}
		if active.RecipeVersion != apptypes.PayloadBackfillRecipeVersion {
			return backfillRunHandle{}, xerrors.Errorf("%w: checkpoint %q current %q", ErrPayloadBackfillRecipeMismatch, active.RecipeVersion, apptypes.PayloadBackfillRecipeVersion)
		}
		// Restart the cursor so a resume cannot complete a mid-pass walk and
		// strand conflicts that were skipped before the pause. No-ops are
		// cheap; missing a conflicted row is silent data loss.
		if err = markBackfillRunningFromOrigin(ctx, db, active.RunID); err != nil {
			return backfillRunHandle{}, err
		}
		active.State = apptypes.PayloadBackfillRunning
		active.CursorRowID = 0
		return backfillRunHandle{backfillRunRow: active, passDirty: false}, nil
	}
	if err == nil {
		return backfillRunHandle{}, ErrPayloadBackfillActiveRun
	}
	highWater, err := maxEventRowID(ctx, db)
	if err != nil {
		return backfillRunHandle{}, err
	}
	runID, err := newPayloadBackfillRunID()
	if err != nil {
		return backfillRunHandle{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.ExecContext(ctx, `
		INSERT INTO payload_backfill_runs(
			run_id, recipe_version, high_water_rowid, cursor_rowid, pass_count, state,
			started_at, updated_at
		) VALUES (?, ?, ?, 0, 0, 'running', ?, ?)`,
		runID, apptypes.PayloadBackfillRecipeVersion, highWater, now, now,
	); err != nil {
		return backfillRunHandle{}, xerrors.Errorf("insert payload backfill run: %w", err)
	}
	return backfillRunHandle{backfillRunRow: backfillRunRow{
		RunID: runID, RecipeVersion: apptypes.PayloadBackfillRecipeVersion,
		HighWaterRowID: highWater, State: apptypes.PayloadBackfillRunning,
		StartedAt: now, UpdatedAt: now,
	}}, nil
}

func requirePayloadBackfillSchema(ctx context.Context, db *sql.DB) error {
	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='payload_backfill_runs'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPayloadBackfillTableMissing
	}
	if err != nil {
		return xerrors.Errorf("inspect payload backfill schema: %w", err)
	}
	return nil
}

func maxEventRowID(ctx context.Context, db *sql.DB) (int64, error) {
	var high sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(rowid) FROM events`).Scan(&high); err != nil {
		return 0, xerrors.Errorf("read events high-water rowid: %w", err)
	}
	if !high.Valid {
		return 0, nil
	}
	return high.Int64, nil
}

// eligiblePredicate selects rows the recipe may still rewrite. Full identity
// rows remain selected so a multi-hour run compresses post-v0.34 writes; the
// fixpoint loop terminates when a full pass rewrites nothing and skips no
// conflicts (incompressible identity is a no-op rewrite).
const eligibleBackfillPredicate = `(body_codec IS NULL OR body_codec = 'identity')`

func countEligibleBackfillRows(ctx context.Context, db *sql.DB, afterRowID, highWater int64) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		 WHERE rowid > ? AND rowid <= ?
		   AND `+eligibleBackfillPredicate, afterRowID, highWater).Scan(&n); err != nil {
		return 0, xerrors.Errorf("count eligible payload backfill rows: %w", err)
	}
	return n, nil
}

func selectBackfillBatch(ctx context.Context, db *sql.DB, afterRowID, highWater int64, limit int) ([]backfillCandidate, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rowid, id, body,
		       body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		  FROM events
		 WHERE rowid > ? AND rowid <= ?
		   AND `+eligibleBackfillPredicate+`
		 ORDER BY rowid
		 LIMIT ?`, afterRowID, highWater, limit)
	if err != nil {
		return nil, xerrors.Errorf("select payload backfill batch: %w", err)
	}
	defer func() { _ = rows.Close() }()

	batch := make([]backfillCandidate, 0, limit)
	for rows.Next() {
		var c backfillCandidate
		var body []byte
		if err = rows.Scan(
			&c.RowID, &c.EventID, &body,
			&c.Row.Codec, &c.Row.FormatVersion, &c.Row.PlaintextBytes, &c.Row.StoredBytes, &c.Row.SHA256,
		); err != nil {
			return nil, xerrors.Errorf("scan payload backfill row: %w", err)
		}
		c.Stored = bytes.Clone(body)
		c.Row.Stored = c.Stored
		sum := sha256.Sum256(c.Stored)
		c.SourceSHA = hex.EncodeToString(sum[:])
		batch = append(batch, c)
	}
	if err = rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate payload backfill batch: %w", err)
	}
	return batch, nil
}

func commitBackfillBatch(ctx context.Context, db *sql.DB, run backfillRunHandle, batch []backfillCandidate) (backfillBatchStats, error) {
	stats := backfillBatchStats{}
	if len(batch) == 0 {
		return stats, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, xerrors.Errorf("begin payload backfill batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, candidate := range batch {
		stats.Scanned++
		if candidate.RowID > stats.LastCursor {
			stats.LastCursor = candidate.RowID
		}

		current, err := reselectBackfillRow(ctx, tx, candidate.RowID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				stats.Conflicted++
				continue
			}
			return stats, err
		}
		// Conflict: stored representation changed under us.
		currentSum := sha256.Sum256(current.Stored)
		if hex.EncodeToString(currentSum[:]) != candidate.SourceSHA {
			stats.Conflicted++
			continue
		}
		// Selection may have drifted (codec changed to zstd by another writer).
		if current.Row.Codec.Valid && current.Row.Codec.String != payloadCodecIdentity {
			stats.Conflicted++
			continue
		}

		metaCount := payloadMetadataCount(current.Row)
		if metaCount != 0 && metaCount != 5 {
			stats.Partial++
			stats.PartialEventID = current.EventID
			stats.PartialReason = "incomplete metadata"
			// Leave the row untouched; fail the run after rolling back writes
			// in this batch... Spec: row untouched, run fails closed.
			// Roll back the whole batch so no partial progress lands with a
			// failure naming this row, then the outer failBackfillRun records it.
			_ = tx.Rollback()
			return stats, ErrPayloadBackfillPartialMetadata
		}

		plaintext, err := current.Row.decode(maxDecodedPayloadBytes)
		if err != nil {
			return stats, xerrors.Errorf("decode event %s for payload backfill: %w", current.EventID, err)
		}

		encoded, err := encodePayload(plaintext, payloadCodecZstd)
		if err != nil {
			return stats, xerrors.Errorf("encode event %s for payload backfill: %w", current.EventID, err)
		}

		// Recipe: shrink → zstd; otherwise keep identity with full metadata.
		target := encoded
		if encoded.StoredBytes >= encoded.PlaintextBytes {
			identity, idErr := encodePayload(plaintext, payloadCodecIdentity)
			if idErr != nil {
				return stats, xerrors.Errorf("encode identity event %s for payload backfill: %w", current.EventID, idErr)
			}
			target = identity
		}

		// No-op when the row already carries identical codec metadata and body.
		if backfillRowAlreadyMatches(current.Row, target) {
			continue
		}

		// Bind body as []byte so SQLite stores a BLOB (corruption modes are real).
		if _, err = tx.ExecContext(ctx, `
			UPDATE events
			   SET body = ?,
			       body_codec = ?,
			       body_format_version = ?,
			       body_plaintext_bytes = ?,
			       body_encoded_bytes = ?,
			       body_sha256 = ?
			 WHERE rowid = ?`,
			target.Bytes, target.Codec, target.FormatVersion,
			target.PlaintextBytes, target.StoredBytes, target.SHA256,
			current.RowID,
		); err != nil {
			return stats, xerrors.Errorf("rewrite event %s body: %w", current.EventID, err)
		}
		stats.Rewritten++
		stats.PlaintextBytes += target.PlaintextBytes
		stats.StoredBytes += target.StoredBytes
		if target.Codec == payloadCodecZstd {
			stats.Encoded++
		} else {
			stats.IdentityKept++
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET cursor_rowid = ?,
		       pass_count = ?,
		       scanned_rows = scanned_rows + ?,
		       encoded_rows = encoded_rows + ?,
		       identity_kept_rows = identity_kept_rows + ?,
		       conflicted_rows = conflicted_rows + ?,
		       rewritten_rows = rewritten_rows + ?,
		       plaintext_bytes = plaintext_bytes + ?,
		       stored_bytes = stored_bytes + ?,
		       state = 'running',
		       updated_at = ?
		 WHERE run_id = ?`,
		stats.LastCursor, run.PassCount,
		stats.Scanned, stats.Encoded, stats.IdentityKept, stats.Conflicted, stats.Rewritten,
		stats.PlaintextBytes, stats.StoredBytes, now, run.RunID,
	); err != nil {
		return stats, xerrors.Errorf("advance payload backfill checkpoint: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return stats, xerrors.Errorf("commit payload backfill batch: %w", err)
	}
	return stats, nil
}

func reselectBackfillRow(ctx context.Context, tx *sql.Tx, rowID int64) (backfillCandidate, error) {
	var c backfillCandidate
	var body []byte
	err := tx.QueryRowContext(ctx, `
		SELECT rowid, id, body,
		       body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256
		  FROM events
		 WHERE rowid = ?`, rowID).Scan(
		&c.RowID, &c.EventID, &body,
		&c.Row.Codec, &c.Row.FormatVersion, &c.Row.PlaintextBytes, &c.Row.StoredBytes, &c.Row.SHA256,
	)
	if err != nil {
		return backfillCandidate{}, xerrors.Errorf("reselect payload backfill row %d: %w", rowID, err)
	}
	c.Stored = bytes.Clone(body)
	c.Row.Stored = c.Stored
	return c, nil
}

func payloadMetadataCount(r payloadRow) int {
	n := 0
	for _, valid := range []bool{r.Codec.Valid, r.FormatVersion.Valid, r.PlaintextBytes.Valid, r.StoredBytes.Valid, r.SHA256.Valid} {
		if valid {
			n++
		}
	}
	return n
}

func backfillRowAlreadyMatches(r payloadRow, target encodedPayload) bool {
	if payloadMetadataCount(r) != 5 {
		return false
	}
	if !r.Codec.Valid || r.Codec.String != target.Codec {
		return false
	}
	if !r.FormatVersion.Valid || int(r.FormatVersion.Int64) != target.FormatVersion {
		return false
	}
	if !r.PlaintextBytes.Valid || r.PlaintextBytes.Int64 != target.PlaintextBytes {
		return false
	}
	if !r.StoredBytes.Valid || r.StoredBytes.Int64 != target.StoredBytes {
		return false
	}
	if !r.SHA256.Valid || r.SHA256.String != target.SHA256 {
		return false
	}
	return bytes.Equal(r.Stored, target.Bytes)
}

func loadActiveBackfillRun(ctx context.Context, db *sql.DB) (backfillRunRow, error) {
	return scanBackfillRun(db.QueryRowContext(ctx, `
		SELECT run_id, recipe_version, high_water_rowid, cursor_rowid, pass_count, state,
		       started_at, updated_at, completed_at,
		       scanned_rows, encoded_rows, identity_kept_rows, conflicted_rows,
		       partial_metadata_rows, rewritten_rows, plaintext_bytes, stored_bytes,
		       failure_event_id, failure_reason
		  FROM payload_backfill_runs
		 WHERE state IN ('running', 'paused')
		 ORDER BY started_at DESC
		 LIMIT 1`))
}

func loadLatestBackfillRun(ctx context.Context, db *sql.DB) (backfillRunRow, error) {
	return scanBackfillRun(db.QueryRowContext(ctx, `
		SELECT run_id, recipe_version, high_water_rowid, cursor_rowid, pass_count, state,
		       started_at, updated_at, completed_at,
		       scanned_rows, encoded_rows, identity_kept_rows, conflicted_rows,
		       partial_metadata_rows, rewritten_rows, plaintext_bytes, stored_bytes,
		       failure_event_id, failure_reason
		  FROM payload_backfill_runs
		 ORDER BY started_at DESC
		 LIMIT 1`))
}

func loadBackfillRun(ctx context.Context, db *sql.DB, runID string) (backfillRunRow, error) {
	return scanBackfillRun(db.QueryRowContext(ctx, `
		SELECT run_id, recipe_version, high_water_rowid, cursor_rowid, pass_count, state,
		       started_at, updated_at, completed_at,
		       scanned_rows, encoded_rows, identity_kept_rows, conflicted_rows,
		       partial_metadata_rows, rewritten_rows, plaintext_bytes, stored_bytes,
		       failure_event_id, failure_reason
		  FROM payload_backfill_runs
		 WHERE run_id = ?`, runID))
}

func scanBackfillRun(row *sql.Row) (backfillRunRow, error) {
	var r backfillRunRow
	var state string
	if err := row.Scan(
		&r.RunID, &r.RecipeVersion, &r.HighWaterRowID, &r.CursorRowID, &r.PassCount, &state,
		&r.StartedAt, &r.UpdatedAt, &r.CompletedAt,
		&r.ScannedRows, &r.EncodedRows, &r.IdentityKeptRows, &r.ConflictedRows,
		&r.PartialMetadataRows, &r.RewrittenRows, &r.PlaintextBytes, &r.StoredBytes,
		&r.FailureEventID, &r.FailureReason,
	); err != nil {
		return backfillRunRow{}, xerrors.Errorf("scan payload backfill run: %w", err)
	}
	r.State = apptypes.PayloadBackfillState(state)
	return r, nil
}

func markBackfillRunningFromOrigin(ctx context.Context, db *sql.DB, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET state = 'running', cursor_rowid = 0, updated_at = ?
		 WHERE run_id = ? AND state IN ('running', 'paused')`, now, runID)
	if err != nil {
		return xerrors.Errorf("mark payload backfill running: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrPayloadBackfillNoResumableRun
	}
	return nil
}

func pauseBackfillRun(ctx context.Context, db *sql.DB, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET state = 'paused', updated_at = ?
		 WHERE run_id = ? AND state = 'running'`, now, runID); err != nil {
		return xerrors.Errorf("pause payload backfill: %w", err)
	}
	return nil
}

func completeBackfillRun(ctx context.Context, db *sql.DB, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET state = 'completed', completed_at = ?, updated_at = ?
		 WHERE run_id = ?`, now, now, runID); err != nil {
		return xerrors.Errorf("complete payload backfill: %w", err)
	}
	return nil
}

func resetBackfillCursor(ctx context.Context, db *sql.DB, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET cursor_rowid = 0, updated_at = ?
		 WHERE run_id = ?`, now, runID); err != nil {
		return xerrors.Errorf("reset payload backfill cursor: %w", err)
	}
	return nil
}

func failBackfillRun(ctx context.Context, db *sql.DB, runID, eventID, reason string, stats backfillBatchStats) (backfillRunRow, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// No row was rewritten in the failed batch (rolled back). Record the
	// partial-metadata count and close the run.
	if _, err := db.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET state = 'failed',
		       partial_metadata_rows = partial_metadata_rows + ?,
		       failure_event_id = ?,
		       failure_reason = ?,
		       completed_at = ?,
		       updated_at = ?
		 WHERE run_id = ?`,
		stats.Partial, eventID, reason, now, now, runID,
	); err != nil {
		return backfillRunRow{}, xerrors.Errorf("fail payload backfill: %w", err)
	}
	return loadBackfillRun(ctx, db, runID)
}

func resultFromRun(run backfillRunRow, batchCount int64, morePending bool) apptypes.PayloadBackfillResult {
	if run.State.CanResume() {
		morePending = true
	}
	if run.State == apptypes.PayloadBackfillCompleted {
		morePending = false
	}
	result := apptypes.PayloadBackfillResult{
		RunID:               run.RunID,
		State:               string(run.State),
		RecipeVersion:       run.RecipeVersion,
		HighWaterRowID:      run.HighWaterRowID,
		CursorRowID:         run.CursorRowID,
		PassCount:           run.PassCount,
		ScannedRows:         run.ScannedRows,
		EncodedRows:         run.EncodedRows,
		IdentityKeptRows:    run.IdentityKeptRows,
		ConflictedRows:      run.ConflictedRows,
		PartialMetadataRows: run.PartialMetadataRows,
		RewrittenRows:       run.RewrittenRows,
		PlaintextBytes:      run.PlaintextBytes,
		StoredBytes:         run.StoredBytes,
		BatchCount:          batchCount,
		MorePending:         morePending,
		StartedAt:           run.StartedAt,
		UpdatedAt:           run.UpdatedAt,
	}
	if run.CompletedAt.Valid {
		result.CompletedAt = run.CompletedAt.String
	}
	if run.FailureEventID.Valid {
		result.FailureEventID = run.FailureEventID.String
	}
	if run.FailureReason.Valid {
		result.FailureReason = run.FailureReason.String
	}
	return result
}

func newPayloadBackfillRunID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", xerrors.Errorf("generate payload backfill run id: %w", err)
	}
	return "backfill-" + hex.EncodeToString(raw), nil
}
