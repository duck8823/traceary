package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
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
	// ErrPayloadBackfillSchemaOutdated means the store applied a pre-release
	// revision of migration 054. Migration 054 is unreleased and was edited in
	// place, so schema_migrations already records version 54 and the migrator
	// will not re-apply it: the run table exists but is missing a column. Only
	// a store built from this branch before the edit can reach this.
	ErrPayloadBackfillSchemaOutdated = errors.New("payload backfill bookkeeping table predates the released migration 054; recreate the store")
	// ErrPayloadBackfillCompatibilityMode refuses to start on a store whose
	// payload codec compatibility evidence is not the counter.
	ErrPayloadBackfillCompatibilityMode = errors.New("payload backfill requires counter-mode payload codec compatibility state")
	// ErrPayloadBackfillRunPreempted means the run left the state this worker
	// was advancing — another resume of the same run terminated it.
	ErrPayloadBackfillRunPreempted = errors.New("payload backfill run was terminated by another worker")
)

// payloadCodecCompatibilityCounterMode is the only compatibility mode whose
// triggers tolerate a codec transition (migration 036 / 043).
const payloadCodecCompatibilityCounterMode = "counter"

// payloadBackfillCheckpointTimeout bounds a terminal transition that runs after
// the caller's context is gone. It is one small UPDATE plus one read, so this
// is generous against the 1s busy timeout while still letting a run that cannot
// take the store lease give up rather than hang.
const payloadBackfillCheckpointTimeout = 10 * time.Second

// PayloadBackfillDatasource rewrites payload text columns in place through the
// codec: events.body and command_audits.{command_text,input_text,output_text}.
type PayloadBackfillDatasource struct {
	db *Database
	// onBeforeCommitBatch is a test hook that fires after a batch is selected
	// and before the verify/encode transaction. Production leaves it nil.
	onBeforeCommitBatch func(batch []backfillCandidate)
	// onAfterCommitBatch is a test hook that fires after a batch commits and
	// before the loop re-checks cancellation. Production leaves it nil.
	onAfterCommitBatch func(batchCount int64)
	// onSelectedBatch is a test-only seam that can rewrite the selector result
	// before the execute loop sees it. Production leaves it nil. Used to force
	// an empty batch while eligible work remains, exercising the fail-safe that
	// refuses completed in that case — without duplicating the candidate SQL.
	onSelectedBatch func(batch []backfillCandidate) []backfillCandidate
}

// NewPayloadBackfillDatasource binds the live store path.
func NewPayloadBackfillDatasource(db *Database) *PayloadBackfillDatasource {
	return &PayloadBackfillDatasource{db: db}
}

var _ application.PayloadBackfill = (*PayloadBackfillDatasource)(nil)

type backfillRunRow struct {
	RunID         string
	RecipeVersion string
	// HighWaterRowID is the inclusive events.rowid ceiling fixed at run start.
	HighWaterRowID int64
	// AuditHighWaterRowID is the inclusive command_audits.rowid ceiling fixed
	// at run start. The two tables use independent rowid sequences, so a shared
	// max would let later inserts into the lagging table land below the ceiling.
	AuditHighWaterRowID int64
	CursorRowID         int64
	PassCount           int64
	State               apptypes.PayloadBackfillState
	WorkerToken         string
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

// backfillLane is one (table, field) unit the recipe rewrites. Order is the
// stable walk order within a shared rowid: body, then the three audit texts.
type backfillLane struct {
	Table       string
	Field       string // logical field name used in errors and result naming
	Column      string // physical TEXT/BLOB column
	CodecPrefix string // prefix of the five codec metadata columns
	PKColumn    string // human-facing row key column (id / event_id)
	Ord         int    // stable order within a rowid
}

// backfillLanes is the full recipe. Preview/run/resume all walk this list.
var backfillLanes = []backfillLane{
	{Table: "events", Field: "body", Column: "body", CodecPrefix: "body", PKColumn: "id", Ord: 0},
	{Table: "command_audits", Field: "command", Column: "command_text", CodecPrefix: "command", PKColumn: "event_id", Ord: 1},
	{Table: "command_audits", Field: "input", Column: "input_text", CodecPrefix: "input", PKColumn: "event_id", Ord: 2},
	{Table: "command_audits", Field: "output", Column: "output_text", CodecPrefix: "output", PKColumn: "event_id", Ord: 3},
}

type backfillCandidate struct {
	Lane      backfillLane
	RowID     int64
	EventID   string // events.id or command_audits.event_id — failure naming
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
	if err = requirePayloadBackfillCounterMode(ctx, db); err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	ceilings, err := readBackfillHighWaters(ctx, db)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	eligible, err := countEligibleBackfillRows(ctx, db, 0, ceilings)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}
	return apptypes.PayloadBackfillResult{
		State:          "planned",
		RecipeVersion:  apptypes.PayloadBackfillRecipeVersion,
		HighWaterRowID: ceilings.Events,
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
	if err = requirePayloadBackfillCounterMode(ctx, db); err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	run, err := prepareBackfillRun(ctx, db, resume)
	if err != nil {
		return apptypes.PayloadBackfillResult{}, err
	}

	// Checkpoint writes outlive the cancellation that triggers them. Every
	// terminal transition — complete, reset, pause, fail — records what the
	// worker already did durably, so running it on the caller's context would
	// lose that record exactly when the run needs it most.
	//
	// Bounded, though: WithoutCancel drops the deadline too, and acquiring the
	// store lease waits on nothing but the context. An unbounded checkpoint
	// would turn "someone else holds the store" into a process that never
	// returns. Losing the checkpoint is recoverable — resume reads the cursor
	// the last committed batch left — and hanging is not.
	closeCtx, cancelClose := context.WithTimeout(context.WithoutCancel(ctx), payloadBackfillCheckpointTimeout)
	defer cancelClose()

	var batchCount int64
	for {
		if err = ctx.Err(); err != nil {
			return d.abortRun(ctx, closeCtx, db, run, batchCount, err)
		}

		// Begin a new pass when the cursor is at the origin.
		if run.CursorRowID == 0 {
			run.PassCount++
			run.passDirty = false
		}

		ceilings := backfillHighWaters{
			Events: run.HighWaterRowID,
			Audits: run.AuditHighWaterRowID,
		}
		batch, selectErr := d.selectBackfillBatch(ctx, db, run.CursorRowID, ceilings, c.BatchRows)
		if selectErr != nil {
			return d.abortRun(ctx, closeCtx, db, run, batchCount, selectErr)
		}
		if d.onSelectedBatch != nil {
			batch = d.onSelectedBatch(batch)
		}
		if len(batch) == 0 {
			// End of a cursor walk. Full identity (incompressible) rows stay
			// selectable forever, so completion is a walk that rewrites nothing
			// and skips no conflicts — not "zero eligible rows".
			//
			// An empty batch with eligible fields still past the cursor is not
			// end-of-walk: refuse completed so unwalked work cannot be reported
			// done. Production selection is a single merged statement; this
			// branch is a fail-safe for a future selector regression (and is
			// exercised by the test-only onSelectedBatch seam).
			if !run.passDirty {
				remaining, countErr := countEligibleBackfillRows(ctx, db, run.CursorRowID, ceilings)
				if countErr != nil {
					return d.abortRun(ctx, closeCtx, db, run, batchCount, countErr)
				}
				if remaining > 0 {
					return d.abortRun(ctx, closeCtx, db, run, batchCount, xerrors.Errorf(
						"payload backfill selection returned no candidates while %d eligible fields remain past cursor %d",
						remaining, run.CursorRowID,
					))
				}
				if completeErr := completeBackfillRun(closeCtx, db, run); completeErr != nil {
					return apptypes.PayloadBackfillResult{}, completeErr
				}
				loaded, loadErr := loadBackfillRun(closeCtx, db, run.RunID)
				if loadErr != nil {
					return apptypes.PayloadBackfillResult{}, loadErr
				}
				return resultFromRun(loaded, batchCount, false), nil
			}
			// Conflicts or rewrites this pass: restart from rowid 0 under the
			// same high-waters so skipped rows are not stranded.
			if resetErr := resetBackfillCursor(closeCtx, db, run); resetErr != nil {
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
				failed, failErr := failBackfillRun(closeCtx, db, run, stats.PartialEventID, stats.PartialReason, stats)
				if failErr != nil {
					return apptypes.PayloadBackfillResult{}, failErr
				}
				return resultFromRun(failed, batchCount, false), xerrors.Errorf("%w: event %s: %s", ErrPayloadBackfillPartialMetadata, stats.PartialEventID, stats.PartialReason)
			}
			return d.abortRun(ctx, closeCtx, db, run, batchCount, commitErr)
		}
		batchCount++
		if d.onAfterCommitBatch != nil {
			d.onAfterCommitBatch(batchCount)
		}
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
			if pauseErr := pauseBackfillRun(closeCtx, db, run); pauseErr != nil {
				return apptypes.PayloadBackfillResult{}, pauseErr
			}
			loaded, loadErr := loadBackfillRun(closeCtx, db, run.RunID)
			if loadErr != nil {
				return apptypes.PayloadBackfillResult{}, loadErr
			}
			return resultFromRun(loaded, batchCount, true), nil
		}
	}
}

// abortRun ends the loop on an error that reached it. When the run was
// cancelled it also lands the pause the cancellation implies: a run left at
// 'running' is resumable, but status reports it active and a new run is
// refused.
//
// The checkpoint and the reported error are separate decisions. A real
// failure — I/O, constraint, decode — that happens to race the cancellation
// still has to reach the operator as itself; reporting it as "cancelled"
// because ctx.Err() was set by then would hide the reason the batch stopped.
// Only a cause that is itself a context error is reported as a cancellation.
func (d *PayloadBackfillDatasource) abortRun(
	ctx context.Context,
	closeCtx context.Context,
	db *sql.DB,
	run backfillRunHandle,
	batchCount int64,
	cause error,
) (apptypes.PayloadBackfillResult, error) {
	if ctx.Err() == nil {
		return apptypes.PayloadBackfillResult{}, cause
	}
	if pauseErr := pauseBackfillRun(closeCtx, db, run); pauseErr != nil {
		return apptypes.PayloadBackfillResult{}, xerrors.Errorf("%v; pause payload backfill: %w", cause, pauseErr)
	}
	if !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return apptypes.PayloadBackfillResult{}, cause
	}
	loaded, loadErr := loadBackfillRun(closeCtx, db, run.RunID)
	if loadErr != nil {
		return apptypes.PayloadBackfillResult{}, loadErr
	}
	return resultFromRun(loaded, batchCount, true), xerrors.Errorf("payload backfill cancelled: %w", cause)
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
		token, tokenErr := newPayloadBackfillRunID()
		if tokenErr != nil {
			return backfillRunHandle{}, tokenErr
		}
		if err = markBackfillRunningFromOrigin(ctx, db, active.RunID, token); err != nil {
			return backfillRunHandle{}, err
		}
		active.State = apptypes.PayloadBackfillRunning
		active.WorkerToken = token
		active.CursorRowID = 0
		return backfillRunHandle{backfillRunRow: active, passDirty: false}, nil
	}
	if err == nil {
		return backfillRunHandle{}, ErrPayloadBackfillActiveRun
	}
	ceilings, err := readBackfillHighWaters(ctx, db)
	if err != nil {
		return backfillRunHandle{}, err
	}
	runID, err := newPayloadBackfillRunID()
	if err != nil {
		return backfillRunHandle{}, err
	}
	token, err := newPayloadBackfillRunID()
	if err != nil {
		return backfillRunHandle{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.ExecContext(ctx, `
		INSERT INTO payload_backfill_runs(
			run_id, recipe_version, high_water_rowid, audit_high_water_rowid,
			cursor_rowid, pass_count, state,
			worker_token, started_at, updated_at
		) VALUES (?, ?, ?, ?, 0, 0, 'running', ?, ?, ?)`,
		runID, apptypes.PayloadBackfillRecipeVersion, ceilings.Events, ceilings.Audits, token, now, now,
	); err != nil {
		return backfillRunHandle{}, xerrors.Errorf("insert payload backfill run: %w", err)
	}
	return backfillRunHandle{backfillRunRow: backfillRunRow{
		RunID: runID, RecipeVersion: apptypes.PayloadBackfillRecipeVersion,
		HighWaterRowID: ceilings.Events, AuditHighWaterRowID: ceilings.Audits,
		State:       apptypes.PayloadBackfillRunning,
		WorkerToken: token, StartedAt: now, UpdatedAt: now,
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
	// Name the pre-release shape rather than letting every query fail with
	// "no such column: worker_token" / "audit_high_water_rowid" far from the cause.
	// Migration 054 is unreleased and was edited in place more than once.
	for _, column := range []string{"worker_token", "audit_high_water_rowid"} {
		exists, colErr := databaseColumnExists(ctx, db, "payload_backfill_runs", column)
		if colErr != nil {
			return xerrors.Errorf("inspect payload backfill schema: %w", colErr)
		}
		if !exists {
			return ErrPayloadBackfillSchemaOutdated
		}
	}
	return nil
}

// requirePayloadBackfillCounterMode refuses to start on a store whose payload
// codec compatibility evidence is not the counter. Migration 036's
// payload_codec_events_update trigger updates the counter row WHERE
// mode='counter' AND state='valid' and then RAISE(ABORT)s if that matched no
// row, so on a legacy_index store (migration 043: a development database that
// applied the original migration 36) every identity -> zstd transition aborts
// its batch with "invalid payload codec compatibility state". Failing here
// names the reason instead of leaving an open run that can never advance.
func requirePayloadBackfillCounterMode(ctx context.Context, db *sql.DB) error {
	var mode, state string
	err := db.QueryRowContext(ctx, `SELECT mode, state FROM payload_codec_compatibility_state WHERE singleton=1`).Scan(&mode, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return xerrors.Errorf("%w: payload codec compatibility state is missing", ErrPayloadBackfillCompatibilityMode)
	}
	if err != nil {
		return xerrors.Errorf("inspect payload codec compatibility state: %w", err)
	}
	if state != "valid" || mode != payloadCodecCompatibilityCounterMode {
		return xerrors.Errorf("%w: mode=%q state=%q", ErrPayloadBackfillCompatibilityMode, mode, state)
	}
	return nil
}

// backfillHighWaters are the inclusive per-table rowid ceilings fixed at run
// start. The shared cursor walks both sequences under one number, but each
// table is bounded by its own ceiling so a later insert into the lagging
// sequence cannot land below a shared max.
type backfillHighWaters struct {
	Events int64
	Audits int64
}

// readBackfillHighWaters measures the ceilings for a new run. Resume must read
// the checkpointed values instead — recomputing would move the boundary.
func readBackfillHighWaters(ctx context.Context, db *sql.DB) (backfillHighWaters, error) {
	var eventHigh, auditHigh sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(rowid) FROM events`).Scan(&eventHigh); err != nil {
		return backfillHighWaters{}, xerrors.Errorf("read events high-water rowid: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT MAX(rowid) FROM command_audits`).Scan(&auditHigh); err != nil {
		return backfillHighWaters{}, xerrors.Errorf("read command_audits high-water rowid: %w", err)
	}
	var ceilings backfillHighWaters
	if eventHigh.Valid {
		ceilings.Events = eventHigh.Int64
	}
	if auditHigh.Valid {
		ceilings.Audits = auditHigh.Int64
	}
	return ceilings, nil
}

// eligibleLanePredicate selects field values the recipe may still rewrite. Full
// identity rows remain selected so a multi-hour run compresses post-v0.34
// writes; the fixpoint loop terminates when a full pass rewrites nothing and
// skips no conflicts (incompressible identity is a no-op rewrite).
//
// The second arm selects fields whose five codec columns are neither all-NULL
// nor all-present, whatever the codec says. Such a field is unreadable, and the
// run is specified to fail closed on it; selecting only identity fields would
// let a corrupt zstd field pass a whole walk uninspected and report completed.
func eligibleLanePredicate(codecPrefix string) string {
	return `(
		   (` + codecPrefix + `_codec IS NULL OR ` + codecPrefix + `_codec = 'identity')
		OR (  (` + codecPrefix + `_codec IS NOT NULL)
		    + (` + codecPrefix + `_format_version IS NOT NULL)
		    + (` + codecPrefix + `_plaintext_bytes IS NOT NULL)
		    + (` + codecPrefix + `_encoded_bytes IS NOT NULL)
		    + (` + codecPrefix + `_sha256 IS NOT NULL)) NOT IN (0, 5)
	)`
}

// anyAuditLaneEligible is the OR of the three command_audits field predicates.
// Used when collecting distinct rowids so a partial-metadata field is not
// stranded because another field of the same row already holds zstd.
func anyAuditLaneEligible() string {
	return `(` + eligibleLanePredicate("command") +
		` OR ` + eligibleLanePredicate("input") +
		` OR ` + eligibleLanePredicate("output") + `)`
}

func laneHighWater(lane backfillLane, ceilings backfillHighWaters) int64 {
	if lane.Table == "command_audits" {
		return ceilings.Audits
	}
	return ceilings.Events
}

func countEligibleBackfillRows(ctx context.Context, db *sql.DB, afterRowID int64, ceilings backfillHighWaters) (int64, error) {
	var total int64
	for _, lane := range backfillLanes {
		var n int64
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM `+lane.Table+`
			 WHERE rowid > ? AND rowid <= ?
			   AND `+eligibleLanePredicate(lane.CodecPrefix), afterRowID, laneHighWater(lane, ceilings)).Scan(&n); err != nil {
			return 0, xerrors.Errorf("count eligible payload backfill rows for %s.%s: %w", lane.Table, lane.Field, err)
		}
		total += n
	}
	return total, nil
}

// selectBackfillBatch walks both tables under one rowid cursor. BatchRows limits
// distinct rowids, not field units: every eligible field of a selected rowid is
// loaded so a LIMIT cut cannot strand command_text while taking body of the
// same numeric rowid (or vice versa).
//
// Pick and expansion are one statement so they share a read snapshot. A split
// that found rowids then expanded them separately could return an empty batch
// after every picked row became ineligible, and execute would treat that empty
// batch as end-of-walk while later eligible rowids remained beyond the cursor.
// Each expansion arm also bounds by its own table ceiling so an events.rowid
// cannot pull a same-numbered command_audits row that is above the audit
// ceiling.
func (d *PayloadBackfillDatasource) selectBackfillBatch(ctx context.Context, db *sql.DB, afterRowID int64, ceilings backfillHighWaters, limit int) ([]backfillCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	// Bound parameters:
	//   ?1 afterRowID (shared cursor)
	//   ?2 events high-water
	//   ?3 command_audits high-water
	//   ?4 batch rowid limit
	parts := make([]string, 0, len(backfillLanes))
	for _, lane := range backfillLanes {
		ceilingParam := "?2"
		if lane.Table == "command_audits" {
			ceilingParam = "?3"
		}
		parts = append(parts, `
			SELECT rowid, `+strconv.Itoa(lane.Ord)+` AS lane_ord, `+quoteSQLString(lane.Field)+` AS field,
			       `+lane.PKColumn+` AS row_key, `+lane.Column+` AS stored,
			       `+lane.CodecPrefix+`_codec, `+lane.CodecPrefix+`_format_version,
			       `+lane.CodecPrefix+`_plaintext_bytes, `+lane.CodecPrefix+`_encoded_bytes,
			       `+lane.CodecPrefix+`_sha256
			  FROM `+lane.Table+`
			 WHERE rowid IN (SELECT rowid FROM picked)
			   AND rowid <= `+ceilingParam+`
			   AND `+eligibleLanePredicate(lane.CodecPrefix))
	}
	query := `
		WITH picked AS (
			SELECT rowid FROM (
				SELECT rowid FROM events
				 WHERE rowid > ?1 AND rowid <= ?2
				   AND ` + eligibleLanePredicate("body") + `
				UNION
				SELECT rowid FROM command_audits
				 WHERE rowid > ?1 AND rowid <= ?3
				   AND ` + anyAuditLaneEligible() + `
			)
			 ORDER BY rowid
			 LIMIT ?4
		)
		` + strings.Join(parts, " UNION ALL ") + `
		 ORDER BY rowid, lane_ord`

	rows, err := db.QueryContext(ctx, query, afterRowID, ceilings.Events, ceilings.Audits, limit)
	if err != nil {
		return nil, xerrors.Errorf("select payload backfill candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanBackfillCandidates(rows, limit)
}

func scanBackfillCandidates(rows *sql.Rows, rowIDHint int) ([]backfillCandidate, error) {
	batch := make([]backfillCandidate, 0, rowIDHint*len(backfillLanes))
	for rows.Next() {
		var (
			c       backfillCandidate
			laneOrd int
			field   string
			stored  []byte
		)
		if err := rows.Scan(
			&c.RowID, &laneOrd, &field, &c.EventID, &stored,
			&c.Row.Codec, &c.Row.FormatVersion, &c.Row.PlaintextBytes, &c.Row.StoredBytes, &c.Row.SHA256,
		); err != nil {
			return nil, xerrors.Errorf("scan payload backfill candidate: %w", err)
		}
		lane, ok := backfillLaneByOrd(laneOrd)
		if !ok || lane.Field != field {
			return nil, xerrors.Errorf("unknown payload backfill lane ord %d field %q", laneOrd, field)
		}
		c.Lane = lane
		c.Stored = bytes.Clone(stored)
		c.Row.Stored = c.Stored
		sum := sha256.Sum256(c.Stored)
		c.SourceSHA = hex.EncodeToString(sum[:])
		batch = append(batch, c)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate payload backfill candidates: %w", err)
	}
	return batch, nil
}

func backfillLaneByOrd(ord int) (backfillLane, bool) {
	for _, lane := range backfillLanes {
		if lane.Ord == ord {
			return lane, true
		}
	}
	return backfillLane{}, false
}

func quoteSQLString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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

		current, err := reselectBackfillCandidate(ctx, tx, candidate)
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
		// Corruption is checked before drift: a field can be both non-identity
		// and incoherent, and skipping it as a conflict would report a clean
		// completion over a field no reader can decode.
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
		// Selection may have drifted (codec changed to zstd by another writer).
		if current.Row.Codec.Valid && current.Row.Codec.String != payloadCodecIdentity {
			stats.Conflicted++
			continue
		}

		plaintext, err := current.Row.decode(maxDecodedPayloadBytes)
		if err != nil {
			return stats, xerrors.Errorf("decode %s %s for payload backfill: %w", current.EventID, current.Lane.Field, err)
		}

		encoded, err := encodeCanonicalPayload(plaintext, true)
		if err != nil {
			return stats, xerrors.Errorf("encode %s %s for payload backfill: %w", current.EventID, current.Lane.Field, err)
		}

		// No-op when the field already carries identical codec metadata and bytes.
		if backfillRowAlreadyMatches(current.Row, encoded) {
			continue
		}

		// Affinity is part of the stored representation, not an encoding
		// detail. zstd bytes must be a BLOB; an identity value must stay TEXT
		// because every other identity writer binds string(...) and because
		// migration 053 still re-derives legacy_source_hook from an identity
		// body, where LIKE only matches TEXT. Rewriting an incompressible
		// legacy row as a BLOB would silently drop it from --source-hook.
		if _, err = tx.ExecContext(ctx, `
			UPDATE `+current.Lane.Table+`
			   SET `+current.Lane.Column+` = ?,
			       `+current.Lane.CodecPrefix+`_codec = ?,
			       `+current.Lane.CodecPrefix+`_format_version = ?,
			       `+current.Lane.CodecPrefix+`_plaintext_bytes = ?,
			       `+current.Lane.CodecPrefix+`_encoded_bytes = ?,
			       `+current.Lane.CodecPrefix+`_sha256 = ?
			 WHERE rowid = ?`,
			storedBodyArg(encoded), encoded.Codec, encoded.FormatVersion,
			encoded.PlaintextBytes, encoded.StoredBytes, encoded.SHA256,
			current.RowID,
		); err != nil {
			return stats, xerrors.Errorf("rewrite %s %s: %w", current.EventID, current.Lane.Field, err)
		}
		stats.Rewritten++
		stats.PlaintextBytes += encoded.PlaintextBytes
		stats.StoredBytes += encoded.StoredBytes
		if encoded.Codec == payloadCodecZstd {
			stats.Encoded++
		} else {
			stats.IdentityKept++
		}
	}

	// The checkpoint carries the ownership assertion: migration 054 admits one
	// active run row, not one worker, so a second resume of the same run would
	// otherwise keep advancing it — and could push a run another worker already
	// failed back to 'running'. Asserting the state inside the batch
	// transaction makes losing the run abort the batch instead.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `
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
		 WHERE run_id = ? AND state = 'running' AND worker_token = ?`,
		stats.LastCursor, run.PassCount,
		stats.Scanned, stats.Encoded, stats.IdentityKept, stats.Conflicted, stats.Rewritten,
		stats.PlaintextBytes, stats.StoredBytes, now, run.RunID, run.WorkerToken,
	)
	if err != nil {
		return stats, xerrors.Errorf("advance payload backfill checkpoint: %w", err)
	}
	if affected, affErr := res.RowsAffected(); affErr != nil {
		return stats, xerrors.Errorf("read payload backfill checkpoint result: %w", affErr)
	} else if affected != 1 {
		return stats, xerrors.Errorf("%w: run %s", ErrPayloadBackfillRunPreempted, run.RunID)
	}
	if err = tx.Commit(); err != nil {
		return stats, xerrors.Errorf("commit payload backfill batch: %w", err)
	}
	return stats, nil
}

// storedBodyArg preserves the column affinity each codec is stored with:
// identity as TEXT (what every other writer binds), zstd as BLOB.
func storedBodyArg(payload encodedPayload) any {
	if payload.Codec == payloadCodecIdentity {
		return string(payload.Bytes)
	}
	return payload.Bytes
}

func reselectBackfillCandidate(ctx context.Context, tx *sql.Tx, candidate backfillCandidate) (backfillCandidate, error) {
	lane := candidate.Lane
	var c backfillCandidate
	var stored []byte
	err := tx.QueryRowContext(ctx, `
		SELECT rowid, `+lane.PKColumn+`, `+lane.Column+`,
		       `+lane.CodecPrefix+`_codec, `+lane.CodecPrefix+`_format_version,
		       `+lane.CodecPrefix+`_plaintext_bytes, `+lane.CodecPrefix+`_encoded_bytes,
		       `+lane.CodecPrefix+`_sha256
		  FROM `+lane.Table+`
		 WHERE rowid = ?`, candidate.RowID).Scan(
		&c.RowID, &c.EventID, &stored,
		&c.Row.Codec, &c.Row.FormatVersion, &c.Row.PlaintextBytes, &c.Row.StoredBytes, &c.Row.SHA256,
	)
	if err != nil {
		return backfillCandidate{}, xerrors.Errorf("reselect payload backfill %s rowid %d: %w", lane.Field, candidate.RowID, err)
	}
	c.Lane = lane
	c.Stored = bytes.Clone(stored)
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
		SELECT run_id, recipe_version, high_water_rowid, audit_high_water_rowid,
		       cursor_rowid, pass_count, state,
		       worker_token, started_at, updated_at, completed_at,
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
		SELECT run_id, recipe_version, high_water_rowid, audit_high_water_rowid,
		       cursor_rowid, pass_count, state,
		       worker_token, started_at, updated_at, completed_at,
		       scanned_rows, encoded_rows, identity_kept_rows, conflicted_rows,
		       partial_metadata_rows, rewritten_rows, plaintext_bytes, stored_bytes,
		       failure_event_id, failure_reason
		  FROM payload_backfill_runs
		 ORDER BY started_at DESC
		 LIMIT 1`))
}

func loadBackfillRun(ctx context.Context, db *sql.DB, runID string) (backfillRunRow, error) {
	return scanBackfillRun(db.QueryRowContext(ctx, `
		SELECT run_id, recipe_version, high_water_rowid, audit_high_water_rowid,
		       cursor_rowid, pass_count, state,
		       worker_token, started_at, updated_at, completed_at,
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
		&r.RunID, &r.RecipeVersion, &r.HighWaterRowID, &r.AuditHighWaterRowID,
		&r.CursorRowID, &r.PassCount, &state,
		&r.WorkerToken, &r.StartedAt, &r.UpdatedAt, &r.CompletedAt,
		&r.ScannedRows, &r.EncodedRows, &r.IdentityKeptRows, &r.ConflictedRows,
		&r.PartialMetadataRows, &r.RewrittenRows, &r.PlaintextBytes, &r.StoredBytes,
		&r.FailureEventID, &r.FailureReason,
	); err != nil {
		return backfillRunRow{}, xerrors.Errorf("scan payload backfill run: %w", err)
	}
	r.State = apptypes.PayloadBackfillState(state)
	return r, nil
}

// markBackfillRunningFromOrigin claims the run for this worker. Re-stamping the
// token fences whichever worker held it before, so a takeover of a run whose
// process is still alive aborts that worker's next checkpoint.
func markBackfillRunningFromOrigin(ctx context.Context, db *sql.DB, runID, token string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `
		UPDATE payload_backfill_runs
		   SET state = 'running', cursor_rowid = 0, worker_token = ?, updated_at = ?
		 WHERE run_id = ? AND state IN ('running', 'paused')`, token, now, runID)
	if err != nil {
		return xerrors.Errorf("mark payload backfill running: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrPayloadBackfillNoResumableRun
	}
	return nil
}

func pauseBackfillRun(ctx context.Context, db *sql.DB, run backfillRunHandle) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return execOwnedBackfillRun(ctx, db, run.RunID, `
		UPDATE payload_backfill_runs
		   SET state = 'paused', updated_at = ?
		 WHERE run_id = ? AND state = 'running' AND worker_token = ?`, now, run.RunID, run.WorkerToken)
}

// completeBackfillRun and the two helpers below only move a run this worker is
// still advancing. Without the state condition a slower worker could revive a
// run another one already failed, misreport it in status, and block a new run.
func completeBackfillRun(ctx context.Context, db *sql.DB, run backfillRunHandle) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return execOwnedBackfillRun(ctx, db, run.RunID, `
		UPDATE payload_backfill_runs
		   SET state = 'completed', completed_at = ?, updated_at = ?
		 WHERE run_id = ? AND state = 'running' AND worker_token = ?`, now, now, run.RunID, run.WorkerToken)
}

func resetBackfillCursor(ctx context.Context, db *sql.DB, run backfillRunHandle) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return execOwnedBackfillRun(ctx, db, run.RunID, `
		UPDATE payload_backfill_runs
		   SET cursor_rowid = 0, updated_at = ?
		 WHERE run_id = ? AND state = 'running' AND worker_token = ?`, now, run.RunID, run.WorkerToken)
}

func execOwnedBackfillRun(ctx context.Context, db *sql.DB, runID, query string, args ...any) error {
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return xerrors.Errorf("advance payload backfill run: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return xerrors.Errorf("read payload backfill run result: %w", err)
	}
	if affected != 1 {
		return xerrors.Errorf("%w: run %s", ErrPayloadBackfillRunPreempted, runID)
	}
	return nil
}

func failBackfillRun(ctx context.Context, db *sql.DB, run backfillRunHandle, eventID, reason string, stats backfillBatchStats) (backfillRunRow, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// No row was rewritten in the failed batch (rolled back). Record the
	// partial-metadata count and close the run. Losing the run here means the
	// failure was never recorded, so it is reported as preemption rather than
	// as a failure the caller can act on.
	if err := execOwnedBackfillRun(ctx, db, run.RunID, `
		UPDATE payload_backfill_runs
		   SET state = 'failed',
		       partial_metadata_rows = partial_metadata_rows + ?,
		       failure_event_id = ?,
		       failure_reason = ?,
		       completed_at = ?,
		       updated_at = ?
		 WHERE run_id = ? AND state = 'running' AND worker_token = ?`,
		stats.Partial, eventID, reason, now, now, run.RunID, run.WorkerToken,
	); err != nil {
		return backfillRunRow{}, err
	}
	return loadBackfillRun(ctx, db, run.RunID)
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
