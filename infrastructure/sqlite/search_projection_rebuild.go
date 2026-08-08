package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domaintypes "github.com/duck8823/traceary/domain/types"
)

const searchProjectionVersion = 1
const searchProjectionKeywordVersion = 1
const searchProjectionSummaryVersion = 1

// searchProjectionIndexFamilyName is the durable label for physical bytes of
// the bounded projection (search_projection_* + literal_search_*). It is never
// the legacy migration-032 event_search_* family — that number belongs to #1718.
const searchProjectionIndexFamilyName = "bounded_search_projection"

func generationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

// Start freezes monotonic SQLite rowid membership.
// Inserts after this point belong to the next generation; updates/deletes cause drift.
//
//nolint:wrapcheck,errcheck // SQL errors are returned without losing typed projection errors.
func (d *Database) Start(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if !b.Valid() {
		return apptypes.SearchProjectionGeneration{}, errors.New("search projection budgets must all be positive")
	}
	db, e := d.open(ctx)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer db.Close()
	// Measure before taking the write lock. The dbstat walk is unbounded in the
	// family's own size and would otherwise consume the start budget.
	familyBytesBefore, beforeEvidence := d.measureSearchProjectionFamilyBytes(ctx, db)
	lockCtx, cancel := context.WithTimeout(ctx, b.LockTime)
	defer cancel()
	tx, e := db.BeginTx(lockCtx, nil)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer tx.Rollback()
	g := apptypes.SearchProjectionGeneration{GenerationID: generationID(), ConfigHash: b.ConfigHash()}
	if e = tx.QueryRowContext(lockCtx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&g.SourceRevision); e != nil {
		return g, e
	}
	var requiresInventory int
	if e = tx.QueryRowContext(lockCtx, `SELECT requires_inventory,(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) FROM search_projection_inventory_compat WHERE singleton=1`).Scan(&requiresInventory, &g.HighWater); e != nil {
		return g, e
	}
	if requiresInventory != 0 {
		g.HighWater = 0
	}
	result, e := tx.ExecContext(lockCtx, `UPDATE search_projection_state SET generation_id=?,config_hash=?,source_revision=?,high_water=?,checkpoint=0,phase='source',cleanup_scope='old',failure_class='',state='rebuilding',recent_age_seconds=?,recent_byte_limit=?,cutover_index_family=?,cutover_family_bytes_before=?,cutover_family_bytes_after=0,cutover_before_evidence_status=?,cutover_before_evidence_reason=?,cutover_after_evidence_status='',cutover_after_evidence_reason='',updated_at=? WHERE singleton=1 AND state<>'rebuilding'`, g.GenerationID, g.ConfigHash, g.SourceRevision, g.HighWater, int64(b.RecentAge/time.Second), b.RecentBytes, searchProjectionIndexFamilyName, familyBytesBefore, beforeEvidence.Status, beforeEvidence.Reason, formatTimestamp(now.UTC()))
	if e == nil {
		if n, x := result.RowsAffected(); x != nil || n != 1 {
			return g, &apptypes.SearchProjectionNoProgressError{Reason: "a generation is already rebuilding"}
		}
		inventoryState := "complete"
		if requiresInventory != 0 {
			inventoryState = "rebuilding"
		}
		if _, x := tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET generation_id=?,cursor='',cursor_started=0,state=? WHERE singleton=1`, g.GenerationID, inventoryState); x != nil {
			return g, x
		}
		if _, x := tx.ExecContext(lockCtx, `UPDATE literal_search_projection_state SET generation_id=?,high_water=?,fingerprint_version=1,state='rebuilding',updated_at=? WHERE singleton=1`, g.GenerationID, g.HighWater, formatTimestamp(now.UTC())); x != nil {
			return g, x
		}
		if _, x := tx.ExecContext(lockCtx, `INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water) VALUES(?,'rebuilding',?,?,?)`, g.GenerationID, g.ConfigHash, g.SourceRevision, g.HighWater); x != nil {
			return g, x
		}
		e = tx.Commit()
	}
	return g, e
}

// SelectInventory reads a stable event-ID keyset without holding a write lock.
// Identity bytes use StoredBytes and insertion bytes use WriteBytes, keeping
// this phase under the same reviewed generation budget as payload projection.
//
//nolint:wrapcheck,errcheck // SQL errors preserve the typed inventory contract.
func (d *Database) SelectInventory(ctx context.Context, b apptypes.SearchProjectionBudget) (out apptypes.SearchProjectionInventorySnapshot, err error) {
	db, err := d.open(ctx)
	if err != nil {
		return out, err
	}
	defer db.Close()
	var state, phase string
	if err = db.QueryRowContext(ctx, `SELECT s.generation_id,s.config_hash,s.source_revision,i.cursor,i.cursor_started,s.state,i.state FROM search_projection_state s JOIN search_projection_inventory_state i ON i.singleton=s.singleton WHERE s.singleton=1`).Scan(&out.Generation.GenerationID, &out.Generation.ConfigHash, &out.Generation.SourceRevision, &out.Cursor, &out.CursorStarted, &state, &phase); err != nil {
		return out, err
	}
	if state != "rebuilding" || phase != "rebuilding" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "historical inventory is not rebuilding"}
	}
	if out.Generation.ConfigHash != b.ConfigHash() {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&revision); err != nil {
		return out, err
	}
	if revision != out.Generation.SourceRevision {
		if err = markProjectionDrifted(ctx, db, out.Generation.GenerationID); err != nil {
			return out, err
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	query := `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id ORDER BY e.id LIMIT ?`
	args := []any{b.Rows + 1}
	if out.CursorStarted {
		query = `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id WHERE e.id>? ORDER BY e.id LIMIT ?`
		args = []any{out.Cursor, b.Rows + 1}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var stored, written int64
	for rows.Next() {
		var id string
		var missing bool
		if err = rows.Scan(&id, &missing); err != nil {
			return out, err
		}
		identityBytes := int64(len(id))
		logicalBytes := int64(0)
		if missing {
			logicalBytes = identityBytes + 16
		}
		if len(out.Items) >= b.Rows || stored+identityBytes > b.StoredBytes || written+logicalBytes > b.WriteBytes {
			break
		}
		out.Items = append(out.Items, apptypes.SearchProjectionInventoryItem{EventID: id, LogicalBytes: logicalBytes, Missing: missing})
		stored += identityBytes
		written += logicalBytes
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) == 0 {
		var next string
		var missing bool
		nextQuery := `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id ORDER BY e.id LIMIT 1`
		nextArgs := []any{}
		if out.CursorStarted {
			nextQuery = `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id WHERE e.id>? ORDER BY e.id LIMIT 1`
			nextArgs = []any{out.Cursor}
		}
		err = db.QueryRowContext(ctx, nextQuery, nextArgs...).Scan(&next, &missing)
		if err == nil {
			class, bytes, limit := "inventory_stored_bytes", int64(len(next)), b.StoredBytes
			if bytes <= limit && missing {
				class, bytes, limit = "inventory_write_bytes", int64(len(next))+16, b.WriteBytes
			}
			return out, &apptypes.SearchProjectionOversizeError{Class: class, Bytes: bytes, Limit: limit}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		out.Done = true
		return out, nil
	}
	var remaining int
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id>? LIMIT 1)`, out.Items[len(out.Items)-1].EventID).Scan(&remaining); err != nil {
		return out, err
	}
	out.Done = remaining == 0
	return out, nil
}

// ApplyInventoryBatch atomically persists the admitted identities and cursor.
// The final batch freezes high-water in the same transaction.
//
//nolint:wrapcheck,errcheck // SQL errors preserve the typed inventory contract; rollback is best effort.
func (d *Database) ApplyInventoryBatch(ctx context.Context, p apptypes.SearchProjectionInventoryPlan, lock time.Duration, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	lockCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	for {
		remaining := lock
		if deadline, ok := lockCtx.Deadline(); ok {
			remaining = time.Until(deadline)
		}
		if remaining <= 0 {
			return out, &apptypes.SearchProjectionNoProgressError{Reason: "inventory lock duration cap exceeded"}
		}
		out, err = d.applyInventoryBatchOnce(lockCtx, p, remaining, now)
		if err == nil || !isSearchProjectionSQLiteBusy(err) {
			return out, err
		}
		select {
		case <-lockCtx.Done():
			return out, &apptypes.SearchProjectionNoProgressError{Reason: "inventory lock duration cap exceeded"}
		case <-time.After(min(5*time.Millisecond, remaining)):
		}
	}
}

//nolint:wrapcheck,errcheck // SQL errors preserve the typed inventory contract; rollback is best effort.
func (d *Database) applyInventoryBatchOnce(ctx context.Context, p apptypes.SearchProjectionInventoryPlan, lock time.Duration, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	lockCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	db, err := d.open(lockCtx)
	if err != nil {
		return out, err
	}
	defer db.Close()
	tx, err := db.BeginTx(lockCtx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var revision int64
	var cursor, phase string
	var cursorStarted bool
	if err = tx.QueryRowContext(lockCtx, `SELECT s.source_revision,i.cursor,i.cursor_started,i.state FROM search_projection_state s JOIN search_projection_inventory_state i ON i.singleton=s.singleton WHERE s.generation_id=? AND s.state='rebuilding' AND i.generation_id=s.generation_id`, p.GenerationID).Scan(&revision, &cursor, &cursorStarted, &phase); err != nil {
		return out, err
	}
	var globalRevision int64
	if err = tx.QueryRowContext(lockCtx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&globalRevision); err != nil {
		return out, err
	}
	if phase != "rebuilding" || revision != p.ExpectedRevision || cursor != p.ExpectedCursor || cursorStarted != p.ExpectedCursorStarted {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if globalRevision != p.ExpectedRevision {
		var driftResult sql.Result
		if driftResult, err = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if rows, rowsErr := driftResult.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if driftResult, err = tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if rows, rowsErr := driftResult.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if driftResult, err = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if rows, rowsErr := driftResult.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	for _, item := range p.Items {
		if !item.Missing {
			continue
		}
		if _, err = tx.ExecContext(lockCtx, `INSERT OR IGNORE INTO search_projection_source_sequence(event_id) SELECT id FROM events WHERE id=?`, item.EventID); err != nil {
			return out, err
		}
	}
	nextCursor := p.ExpectedCursor
	if p.NextCursor != "" {
		nextCursor = p.NextCursor
	}
	if p.Done {
		var result sql.Result
		if result, err = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET high_water=(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence),checkpoint=0,phase='source',updated_at=? WHERE generation_id=? AND source_revision=? AND state='rebuilding'`, formatTimestamp(now), p.GenerationID, p.ExpectedRevision); err != nil {
			return out, err
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if result, err = tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET cursor='',cursor_started=0,state='complete' WHERE singleton=1 AND generation_id=? AND cursor=? AND cursor_started=? AND state='rebuilding'`, p.GenerationID, p.ExpectedCursor, p.ExpectedCursorStarted); err != nil {
			return out, err
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if result, err = tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_compat SET requires_inventory=0 WHERE singleton=1 AND requires_inventory=1`); err != nil {
			return out, err
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if _, err = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET high_water=(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(lockCtx, `UPDATE literal_search_projection_state SET high_water=(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
	} else {
		result, updateErr := tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET cursor=?,cursor_started=? WHERE singleton=1 AND generation_id=? AND cursor=? AND cursor_started=? AND state='rebuilding'`, nextCursor, p.NextCursorStarted, p.GenerationID, p.ExpectedCursor, p.ExpectedCursorStarted)
		if updateErr != nil {
			return out, updateErr
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Selected = len(p.Items)
	for _, item := range p.Items {
		if item.Missing {
			out.Written++
		}
	}
	out.StoredBytes, out.WrittenBytes = p.Ledger.StoredBytes, p.Ledger.LogicalWriteBytes
	out.GenerationID = p.GenerationID
	return out, nil
}

// SelectSnapshot performs bounded reads and canonical hydration. Every read uses
// the caller's wall-time context; no transaction or lock is held.
//
//nolint:wrapcheck,errcheck // SQL and typed application errors cross this adapter unchanged.
func (d *Database) SelectSnapshot(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (out apptypes.ProjectionSnapshot, err error) {
	db, e := d.open(ctx)
	if e != nil {
		return out, e
	}
	defer db.Close()
	var state, cleanupScope string
	if e = db.QueryRowContext(ctx, `SELECT generation_id,config_hash,source_revision,high_water,checkpoint,state,phase,cleanup_scope FROM search_projection_state WHERE singleton=1`).Scan(&out.Generation.GenerationID, &out.Generation.ConfigHash, &out.Generation.SourceRevision, &out.Generation.HighWater, &out.Generation.Checkpoint, &state, &out.Phase, &cleanupScope); e != nil {
		return out, e
	}
	out.Now = now.UTC()
	out.CleanupAll = cleanupScope == "all"
	if state != "rebuilding" && (state != "drifted" || out.Phase != "cleanup") {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "no generation has been started"}
	}
	if out.Generation.ConfigHash != b.ConfigHash() {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	var revision int64
	if e = db.QueryRowContext(ctx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&revision); e != nil {
		return out, e
	}
	if revision != out.Generation.SourceRevision && !out.CleanupAll {
		if e = markProjectionDrifted(ctx, db, out.Generation.GenerationID); e != nil {
			return out, e
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if out.Phase != "source" {
		return selectProjectionCleanup(ctx, db, out, b, now)
	}
	rows, e := db.QueryContext(ctx, `SELECT q.sequence,COALESCE(e.id,q.event_id),COALESCE(e.session_id,''),COALESCE(e.created_at_norm,''),COALESCE(e.body_availability,''),e.id IS NULL,COALESCE(length(CAST(e.body AS BLOB)),0)+COALESCE(length(CAST(a.command_text AS BLOB)),0)+COALESCE(length(CAST(a.input_text AS BLOB)),0)+COALESCE(length(CAST(a.output_text AS BLOB)),0),CASE WHEN e.body_availability='available' THEN COALESCE(e.body_plaintext_bytes,e.body_stored_bytes,length(CAST(e.body AS BLOB)),0) ELSE 0 END+COALESCE(a.command_plaintext_bytes,length(CAST(a.command_text AS BLOB)),0)+COALESCE(a.input_plaintext_bytes,length(CAST(a.input_text AS BLOB)),0)+COALESCE(a.output_plaintext_bytes,length(CAST(a.output_text AS BLOB)),0) FROM search_projection_source_sequence q LEFT JOIN events e ON e.id=q.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE q.sequence>? AND q.sequence<=? ORDER BY q.sequence LIMIT ?`, out.Generation.Checkpoint, out.Generation.HighWater, b.Rows)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	var selection apptypes.BudgetLedger
	for rows.Next() {
		var x apptypes.ProjectionDocument
		var avail string
		if e = rows.Scan(&x.Sequence, &x.EventID, &x.SessionID, &x.CreatedAt, &avail, &x.Deleted, &x.StoredBytes, &x.DecodedBytes); e != nil {
			return out, e
		}
		admitted, admissionErr := selection.AdmitSource(b, x.StoredBytes, x.DecodedBytes)
		if admissionErr != nil {
			return out, admissionErr
		}
		if !admitted {
			break
		}
		if !x.Deleted {
			var body string
			if avail == "available" {
				plain, xerr := loadEventPlaintext(ctx, db, x.EventID)
				if xerr != nil {
					return out, xerr
				}
				body, _ = visibleEventBody(string(plain), domaintypes.BodyAvailabilityAvailable)
			}
			cmd, xerr := hydrateAuditPayload(ctx, db, x.EventID, "command")
			if xerr != nil {
				return out, xerr
			}
			in, xerr := hydrateAuditPayload(ctx, db, x.EventID, "input")
			if xerr != nil {
				return out, xerr
			}
			output, xerr := hydrateAuditPayload(ctx, db, x.EventID, "output")
			if xerr != nil {
				return out, xerr
			}
			parts := []string{}
			for _, v := range []string{body, cmd.String, in.String, output.String} {
				if v != "" {
					parts = append(parts, v)
				}
			}
			x.Text = strings.Join(parts, "\n")
			if cmd.Valid && cmd.String != "" {
				x.CommandCount = 1
			}
			_ = db.QueryRowContext(ctx, `SELECT COALESCE(failed,0) FROM command_audits WHERE event_id=?`, x.EventID).Scan(&x.FailureCount)
			_ = db.QueryRowContext(ctx, `SELECT summary_text FROM search_projection_session_summaries WHERE generation_id=? AND session_id=?`, out.Generation.GenerationID, x.SessionID).Scan(&x.PreviousSummary)
		}
		out.Documents = append(out.Documents, x)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	out.SourceDone = len(out.Documents) == 0 || out.Documents[len(out.Documents)-1].Sequence == out.Generation.HighWater
	return out, nil
}

//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func selectProjectionCleanup(ctx context.Context, db *sql.DB, out apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.ProjectionSnapshot, error) {
	var q string
	var args []any
	if out.Phase == "eviction" {
		q = `WITH ordered AS (
 SELECT document_id,created_at_norm,event_id,
 length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(created_at_norm AS BLOB))+2*length(CAST(body_text AS BLOB))+32 logical_bytes,
 COALESCE(SUM(decoded_bytes) OVER (ORDER BY created_at_norm,event_id,document_id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) removed_before,
 SUM(decoded_bytes) OVER () total_bytes
 FROM search_projection_recent_documents WHERE generation_id=?
) SELECT document_id,logical_bytes FROM ordered
 WHERE created_at_norm<? OR total_bytes-removed_before>?
 ORDER BY created_at_norm,event_id,document_id LIMIT ?`
		args = []any{out.Generation.GenerationID, projectionCutoff(now.Add(-b.RecentAge)), b.RecentBytes, b.Rows + 1}
	} else if out.CleanupAll {
		q = `SELECT 'recent',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(created_at_norm AS BLOB))+2*length(CAST(body_text AS BLOB))+32 FROM search_projection_recent_documents UNION ALL SELECT 'summary',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(summary_text AS BLOB))+24 FROM search_projection_session_summaries UNION ALL SELECT 'aggregate',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+16 FROM search_projection_command_aggregates UNION ALL SELECT 'keyword',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(keyword AS BLOB))+16 FROM search_projection_session_keywords UNION ALL SELECT 'fingerprint',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16 FROM literal_search_fingerprints LIMIT ?`
		args = []any{b.Rows + 1}
	} else {
		q = `SELECT 'recent',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(created_at_norm AS BLOB))+2*length(CAST(body_text AS BLOB))+32 FROM search_projection_recent_documents WHERE generation_id<>? UNION ALL SELECT 'summary',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(summary_text AS BLOB))+24 FROM search_projection_session_summaries WHERE generation_id<>? UNION ALL SELECT 'aggregate',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+16 FROM search_projection_command_aggregates WHERE generation_id<>? UNION ALL SELECT 'keyword',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(keyword AS BLOB))+16 FROM search_projection_session_keywords WHERE generation_id<>? UNION ALL SELECT 'fingerprint',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16 FROM literal_search_fingerprints WHERE generation_id<>? LIMIT ?`
		args = []any{out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, b.Rows + 1}
	}
	rows, e := db.QueryContext(ctx, q, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var c apptypes.ProjectionCleanupCandidate
		if out.Phase == "cleanup" {
			e = rows.Scan(&c.Class, &c.RowID, &c.LogicalBytes)
		} else {
			e = rows.Scan(&c.RowID, &c.LogicalBytes)
			c.Class = out.Phase
		}
		if e != nil {
			return out, e
		}
		out.Cleanup = append(out.Cleanup, c)
	}
	if len(out.Cleanup) <= b.Rows {
		out.CleanupDone = true
	} else {
		out.Cleanup = out.Cleanup[:b.Rows]
	}
	return out, rows.Err()
}

// ApplyBatch starts the lock deadline immediately before BeginTx and persists
// writes, cleanup and phase/checkpoint advancement atomically.
func (d *Database) ApplyBatch(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	if p.Phase != "source" {
		return apptypes.SearchProjectionProgress{}, errors.New("apply batch requires source phase")
	}
	return d.applyProjectionPlanWithRetry(ctx, p, lock, now)
}

// CleanupBatch applies only an application-planned eviction or old-generation batch.
func (d *Database) CleanupBatch(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	if p.Phase == "source" {
		return apptypes.SearchProjectionProgress{}, errors.New("cleanup batch requires cleanup phase")
	}
	return d.applyProjectionPlanWithRetry(ctx, p, lock, now)
}

// applyProjectionPlanWithRetry fences concurrent workers at the persisted
// checkpoint. SQLite busy/snapshot conflicts are retried only inside the
// caller's lock cap; the eventual loser observes the winner's checkpoint and
// returns the domain drift error instead of leaking a driver error.
func (d *Database) applyProjectionPlanWithRetry(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	lockCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	for {
		remaining := lock
		if deadline, ok := lockCtx.Deadline(); ok {
			remaining = time.Until(deadline)
		}
		if remaining <= 0 {
			return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "projection lock duration cap exceeded"}
		}
		out, err := d.applyProjectionPlan(lockCtx, p, remaining, now)
		if err == nil || !isSearchProjectionSQLiteBusy(err) {
			return out, err
		}
		select {
		case <-lockCtx.Done():
			return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Reason: "projection lock duration cap exceeded"}
		case <-time.After(min(5*time.Millisecond, remaining)):
		}
	}
}

func isSearchProjectionSQLiteBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

// MarkFailed makes an oversize generation recoverable without advancing its checkpoint.
//
//nolint:wrapcheck,errcheck // Typed failure transition is preserved.
func (d *Database) MarkFailed(ctx context.Context, generation string, revision int64, class string, now time.Time) error {
	db, err := d.open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE search_projection_state SET state='failed',phase='complete',failure_class=?,updated_at=? WHERE generation_id=? AND source_revision=? AND EXISTS(SELECT 1 FROM search_projection_source_revision WHERE singleton=1 AND revision=?)`, class, formatTimestamp(now), generation, revision, revision)
	if err != nil {
		return err
	}
	if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
		return &apptypes.SearchProjectionDriftError{}
	}
	r, err = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='failed' WHERE generation_id=? AND state='rebuilding'`, generation)
	if err != nil {
		return err
	}
	if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
		return &apptypes.SearchProjectionDriftError{}
	}
	return tx.Commit()
}

//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func (d *Database) applyProjectionPlan(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	started := time.Now()
	db, e := d.open(ctx)
	if e != nil {
		return out, e
	}
	defer db.Close()
	lockCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	tx, e := db.BeginTx(lockCtx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var rev, checkpoint, globalRevision int64
	var phase string
	if e = tx.QueryRowContext(lockCtx, `SELECT source_revision,checkpoint,phase FROM search_projection_state WHERE generation_id=?`, p.GenerationID).Scan(&rev, &checkpoint, &phase); e != nil {
		return out, e
	}
	if rev != p.ExpectedRevision || checkpoint != p.ExpectedCheckpoint || phase != p.Phase {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if e = tx.QueryRowContext(lockCtx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&globalRevision); e != nil {
		return out, e
	}
	if globalRevision != p.ExpectedRevision && !p.AllowRevisionDrift {
		var r sql.Result
		if r, e = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE generation_id=? AND source_revision=?`, p.GenerationID, p.ExpectedRevision); e != nil {
			return out, e
		}
		if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if r, e = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); e != nil {
			return out, e
		}
		if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if e = tx.Commit(); e != nil {
			return out, e
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	for _, w := range p.Writes {
		d := w.Document
		if !d.Deleted {
			if w.RetainRecent {
				// The recent FTS tokenizer is `trigram case_sensitive 1` and
				// queries are ASCII-folded before they are matched, so the
				// indexed text must be folded too — exactly as the legacy
				// event_search_documents writer does. Storing raw text here
				// makes every term containing an uppercase letter unfindable.
				// Folding is length-preserving, so decoded_bytes is unaffected.
				bodyText := lowerSearchASCII(d.Text)
				if _, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, p.GenerationID, d.Sequence, d.EventID, d.CreatedAt, bodyText, len(bodyText)); e != nil {
					return out, e
				}
			}
			if _, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_session_summaries VALUES(?,?,1,?,?,?) ON CONFLICT(generation_id,session_id) DO UPDATE SET event_count=event_count+1,summary_text=excluded.summary_text`, p.GenerationID, d.SessionID, w.Summary, searchProjectionVersion, searchProjectionSummaryVersion); e != nil {
				return out, e
			}
			if _, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_command_aggregates VALUES(?,?,?,?) ON CONFLICT(generation_id,session_id) DO UPDATE SET command_count=command_count+excluded.command_count,failure_count=failure_count+excluded.failure_count`, p.GenerationID, d.SessionID, d.CommandCount, d.FailureCount); e != nil {
				return out, e
			}
			for k, n := range w.Keywords {
				if _, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_session_keywords VALUES(?,?,?,?,?) ON CONFLICT(generation_id,session_id,keyword) DO UPDATE SET occurrences=occurrences+excluded.occurrences`, p.GenerationID, d.SessionID, k, n, searchProjectionKeywordVersion); e != nil {
					return out, e
				}
			}
			for _, fingerprint := range w.LiteralFingerprints {
				if _, e = tx.ExecContext(lockCtx, `INSERT OR IGNORE INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version) VALUES(?,?,?,?,1)`, p.GenerationID, d.Sequence, d.EventID, []byte(fingerprint)); e != nil {
					return out, e
				}
			}
			out.Written++
		}
		out.Selected++
	}
	for _, c := range p.Cleanup {
		var table string
		switch c.Class {
		case "eviction", "recent":
			table = "search_projection_recent_documents"
		case "summary":
			table = "search_projection_session_summaries"
		case "aggregate":
			table = "search_projection_command_aggregates"
		case "keyword":
			table = "search_projection_session_keywords"
		case "fingerprint":
			table = "literal_search_fingerprints"
		default:
			continue
		}
		r, x := tx.ExecContext(lockCtx, `DELETE FROM `+table+` WHERE rowid=?`, c.RowID)
		if x != nil {
			return out, x
		}
		n, _ := r.RowsAffected()
		out.Evicted += int(n)
		out.Cleaned += int(n)
		out.CleanupBytes += c.LogicalBytes
	}
	next := p.Phase
	if p.NextPhase != "" {
		next = p.NextPhase
	}
	state := p.ContinueState
	if state == "" {
		state = "rebuilding"
	}
	if p.Completed {
		state = p.FinalState
		if state == "" {
			state = "complete"
		}
	}
	if p.Completed && state == "complete" {
		if _, e = tx.ExecContext(lockCtx, `UPDATE literal_search_projection_state SET state='complete',updated_at=? WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, formatTimestamp(now), p.GenerationID); e != nil {
			return out, e
		}
	}
	if p.Completed && state == "complete" {
		var lifecycleResult sql.Result
		if lifecycleResult, e = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET state='complete' WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); e != nil {
			return out, e
		}
		if n, rowErr := lifecycleResult.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
	}
	r, e := tx.ExecContext(lockCtx, `UPDATE search_projection_state SET checkpoint=?,phase=?,state=?,active_generation_id=CASE WHEN ? AND ?='complete' THEN generation_id ELSE active_generation_id END,last_batch_milliseconds=?,updated_at=? WHERE generation_id=? AND source_revision=? AND checkpoint=? AND phase=? AND (? OR EXISTS(SELECT 1 FROM search_projection_source_revision WHERE singleton=1 AND revision=?))`, p.NextCheckpoint, next, state, p.Completed, state, time.Since(started).Milliseconds(), formatTimestamp(now), p.GenerationID, p.ExpectedRevision, p.ExpectedCheckpoint, p.Phase, p.AllowRevisionDrift, p.ExpectedRevision)
	if e != nil {
		return out, e
	}
	if n, x := r.RowsAffected(); x != nil || n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	// Cutover evidence is recorded after the completion is durable. Measuring
	// inside that transaction would put an unbounded dbstat walk under the lock
	// budget, and a walk that overran it rolled back a generation that had
	// otherwise finished — leaving the store to repeat the same final batch on
	// every open, forever.
	if p.Completed && state == "complete" {
		d.recordSearchProjectionCutoverEvidence(ctx, db, p.GenerationID, now)
	}
	out.Completed = p.Completed
	out.GenerationID = p.GenerationID
	out.StoredBytes = p.Ledger.StoredBytes
	out.DecodedBytes = p.Ledger.DecodedBytes
	out.WrittenBytes = p.Ledger.LogicalWriteBytes
	return out, nil
}

// AbandonSearchProjection idempotently retires the current incomplete generation.
//
//nolint:wrapcheck,errcheck // Transaction errors remain adapter-owned and typed state errors pass through.
func (d *Database) AbandonSearchProjection(ctx context.Context, now time.Time) (out apptypes.SearchProjectionAbandonResult, err error) {
	db, e := d.open(ctx)
	if e != nil {
		return out, e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var generation, state, active string
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(generation_id,''),state,COALESCE(active_generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&generation, &state, &active); e != nil {
		return out, e
	}
	out.GenerationID = generation
	out.State = "abandoned"
	if generation == "" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "no derived generation exists"}
	}
	var lifecycle string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM search_projection_generation_lifecycle WHERE generation_id=?`, generation).Scan(&lifecycle); e == nil && lifecycle == "abandoned" {
		out.AlreadyAbandoned = true
		return out, tx.Commit()
	}
	if generation == active || state == "complete" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "active complete generation cannot be abandoned"}
	}
	r, e := tx.ExecContext(ctx, `UPDATE search_projection_state SET state='failed',phase='complete',failure_class='abandoned',updated_at=? WHERE singleton=1 AND generation_id=? AND state<>'complete'`, formatTimestamp(now), generation)
	if e != nil {
		return out, e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if r, e = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='abandoned',abandoned_at=? WHERE generation_id=? AND state<>'complete'`, formatTimestamp(now), generation); e != nil {
		return out, e
	}
	if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE literal_search_projection_state SET state='stale',updated_at=? WHERE singleton=1 AND generation_id=?`, formatTimestamp(now), generation); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

// SearchProjectionStatus returns payload-free operational evidence.
//
//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func (d *Database) SearchProjectionStatus(ctx context.Context) (s apptypes.SearchProjectionStatus, err error) {
	started := time.Now()
	db, e := d.openReadOnly(ctx)
	if e != nil {
		return s, e
	}
	defer db.Close()
	s.SchemaVersion = "traceary.search-projection-status/v1"
	s.KeywordVersion = searchProjectionKeywordVersion
	s.FingerprintVersion = 1
	e = db.QueryRowContext(ctx, `SELECT state,phase,projection_version,fts_design,config_hash,source_revision,high_water,checkpoint,state='complete',recent_age_seconds,recent_byte_limit,last_batch_milliseconds,CASE WHEN COALESCE(generation_id,'')='' THEN '' ELSE (SELECT state FROM search_projection_generation_lifecycle WHERE generation_id=search_projection_state.generation_id) END,COALESCE((SELECT abandoned_at FROM search_projection_generation_lifecycle WHERE generation_id=search_projection_state.generation_id),''),COALESCE(cutover_index_family,''),cutover_family_bytes_before,cutover_family_bytes_after,COALESCE(cutover_before_evidence_status,''),COALESCE(cutover_before_evidence_reason,''),COALESCE(cutover_after_evidence_status,''),COALESCE(cutover_after_evidence_reason,''),COALESCE(failure_class,'') FROM search_projection_state WHERE singleton=1`).Scan(&s.State, &s.Phase, &s.ProjectionVersion, &s.FTSDesign, &s.ConfigHash, &s.SourceRevision, &s.HighWater, &s.Checkpoint, &s.Completed, &s.RecentAgeSeconds, &s.RecentByteLimit, &s.LastBatchMilliseconds, &s.LifecycleState, &s.AbandonedAt, &s.CutoverIndexFamily, &s.CutoverFamilyBytesBefore, &s.CutoverFamilyBytesAfter, &s.CutoverBeforeEvidence.Status, &s.CutoverBeforeEvidence.Reason, &s.CutoverAfterEvidence.Status, &s.CutoverAfterEvidence.Reason, &s.FailureClass)
	if e != nil {
		return s, e
	}
	// Method is not persisted: dbstat is the only way this figure is ever
	// produced, so it is derived rather than stored per row.
	for _, evidence := range []*apptypes.CapacityEvidence{&s.CutoverBeforeEvidence, &s.CutoverAfterEvidence} {
		if evidence.Status != "" {
			evidence.Method = "dbstat"
		}
	}
	var inventoryState string
	if e = db.QueryRowContext(ctx, `SELECT state FROM search_projection_inventory_state WHERE singleton=1`).Scan(&inventoryState); e != nil {
		return s, e
	}
	if s.State == "rebuilding" && inventoryState == "rebuilding" {
		s.Phase = "inventory"
	}
	if e = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(decoded_bytes),0),COUNT(*) FROM search_projection_recent_documents WHERE generation_id=(SELECT active_generation_id FROM search_projection_state)`).Scan(&s.RecentBytes, &s.RecentDocuments); e != nil {
		return s, e
	}
	if e = db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(summary_text AS BLOB))),0) FROM search_projection_session_summaries WHERE generation_id=(SELECT active_generation_id FROM search_projection_state)`).Scan(&s.SummarySessions, &s.SummaryLogicalBytes); e != nil {
		return s, e
	}
	if e = db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(keyword AS BLOB))),0) FROM search_projection_session_keywords WHERE generation_id=(SELECT active_generation_id FROM search_projection_state)`).Scan(&s.KeywordRows, &s.KeywordLogicalBytes); e != nil {
		return s, e
	}
	if e = db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16),0) FROM literal_search_fingerprints WHERE generation_id=(SELECT active_generation_id FROM search_projection_state)`).Scan(&s.FingerprintRows, &s.FingerprintLogicalBytes); e != nil {
		return s, e
	}
	s.FTSLogicalBytes = s.RecentBytes
	probeStarted := time.Now()
	var ignored int
	if probeErr := db.QueryRowContext(ctx, `SELECT count(*) FROM search_projection_recent_fts WHERE search_projection_recent_fts MATCH 'traceary_projection_probe_no_payload_7f42'`).Scan(&ignored); probeErr != nil {
		return s, probeErr
	}
	s.MatchProbeMilliseconds = time.Since(probeStarted).Milliseconds()
	var page int64
	if db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&page) == nil {
		if e = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name IN (SELECT name FROM sqlite_schema WHERE name LIKE 'search_projection_%' OR tbl_name LIKE 'search_projection_%' OR name LIKE 'literal_search_%' OR tbl_name LIKE 'literal_search_%')`).Scan(&s.PhysicalBytes); e == nil {
			s.PhysicalEvidence = apptypes.CapacityEvidence{Status: "complete", Method: "dbstat"}
		} else {
			s.PhysicalEvidence = apptypes.CapacityEvidence{Status: "unavailable", Method: "pragma", Reason: "dbstat unavailable"}
			e = nil
		}
	}
	s.InspectionMilliseconds = time.Since(started).Milliseconds()
	return s, e
}

// searchProjectionMeasureTimeout bounds the cutover evidence measurement. The
// dbstat walk costs in proportion to the projection family's own page count —
// measured at 1.44s for an ~880MiB family — so it must never share a deadline
// with the transaction that publishes a completed generation. Exceeding this
// yields unavailable evidence, never a failed generation.
const searchProjectionMeasureTimeout = 3 * time.Second

// searchProjectionEvidenceWriteTimeout is the extra allowance the detached
// evidence context gets for its single-row write, on top of the walk.
const searchProjectionEvidenceWriteTimeout = time.Second

func (d *Database) measureTimeout() time.Duration {
	if d.searchProjectionMeasureTimeoutOverride > 0 {
		return d.searchProjectionMeasureTimeoutOverride
	}
	return searchProjectionMeasureTimeout
}

// measureSearchProjectionFamilyBytes returns dbstat physical bytes for the
// bounded search projection family only (search_projection_* and
// literal_search_*). It deliberately excludes the legacy event_search_* family.
//
// The figure is diagnostic: nothing reads it to decide anything. So this never
// returns an error. Anything that stops it producing a number — a missing
// dbstat module, a slow walk, a cancelled parent — comes back as unavailable
// evidence carrying the reason, which is recorded and logged. Recording a bare
// zero instead would be indistinguishable from a genuinely empty family.
func (d *Database) measureSearchProjectionFamilyBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, apptypes.CapacityEvidence) {
	timeout := d.measureTimeout()
	// WithoutCancel deliberately detaches from the caller's deadline: the batch
	// context is sized for the write lock, and evidence must be measurable on
	// its own terms or reported unavailable — never able to fail the batch.
	measureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	var bytes int64
	err := q.QueryRowContext(measureCtx, `
		SELECT COALESCE(SUM(pgsize), 0)
		  FROM dbstat
		 WHERE name IN (
		         SELECT name
		           FROM sqlite_schema
		          WHERE name LIKE 'search_projection_%'
		             OR tbl_name LIKE 'search_projection_%'
		             OR name LIKE 'literal_search_%'
		             OR tbl_name LIKE 'literal_search_%'
		       )`,
	).Scan(&bytes)
	if err != nil {
		return 0, apptypes.CapacityEvidence{
			Status: searchProjectionEvidenceUnavailable,
			Method: "dbstat",
			Reason: truncateEvidenceReason(err.Error()),
		}
	}
	return bytes, apptypes.CapacityEvidence{Status: searchProjectionEvidenceMeasured, Method: "dbstat"}
}

const (
	searchProjectionEvidenceMeasured    = "measured"
	searchProjectionEvidenceUnavailable = "unavailable"
	maxEvidenceReasonBytes              = 200
)

// recordSearchProjectionCutoverEvidence measures the bounded family and stores
// the result against an already-durable completion. Failure here loses a
// diagnostic figure and nothing else, so it is logged rather than returned —
// the generation is complete either way.
//
// Both the walk and the write run on a context detached from the batch. The
// batch context is sized for one bounded unit of work (one second by default)
// and is routinely near expiry by the time a generation completes; a write that
// inherited it would fail exactly when the walk was slow enough to matter,
// leaving an unrecorded figure sitting at zero.
func (d *Database) recordSearchProjectionCutoverEvidence(ctx context.Context, db *sql.DB, generationID string, now time.Time) {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.measureTimeout()+searchProjectionEvidenceWriteTimeout)
	defer cancel()
	bytes, evidence := d.measureSearchProjectionFamilyBytes(recordCtx, db)
	// Fenced on generation_id as well as active_generation_id: another process
	// may have started a replacement generation since the commit, which moves
	// generation_id on but leaves active_generation_id pointing at this one.
	// Matching on the active pointer alone would overwrite the new generation's
	// before-evidence with this generation's after-evidence.
	if _, err := db.ExecContext(recordCtx, `
		UPDATE search_projection_state
		   SET cutover_family_bytes_after = ?,
		       cutover_after_evidence_status = ?,
		       cutover_after_evidence_reason = ?,
		       updated_at = ?
		 WHERE singleton = 1 AND active_generation_id = ? AND generation_id = ?`,
		bytes, evidence.Status, evidence.Reason, formatTimestamp(now), generationID, generationID,
	); err != nil {
		slog.Warn("search projection cutover evidence not recorded; generation is complete regardless",
			"generation_id", generationID,
			"evidence_status", evidence.Status,
			"error", err,
		)
		return
	}
	if evidence.Status == searchProjectionEvidenceUnavailable {
		slog.Warn("search projection cutover evidence unavailable; family bytes after cutover are not reportable",
			"generation_id", generationID,
			"method", evidence.Method,
			"reason", evidence.Reason,
		)
	}
}

// truncateEvidenceReason keeps a diagnostic reason bounded so a pathological
// driver message cannot bloat the state row.
func truncateEvidenceReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) <= maxEvidenceReasonBytes {
		return trimmed
	}
	return trimmed[:maxEvidenceReasonBytes]
}

// VerifySearchProjectionSessionTier runs a real session-tier query against the
// generation under construction (not necessarily active_generation_id) and
// requires it to return the expected session. This is the pre-cleanup gate:
// reclaiming the previous generation is only safe once the new session tier
// answers. An empty generation (no joinable summaries) passes vacuously.
//
//nolint:wrapcheck // Typed projection errors cross this adapter unchanged.
func (d *Database) VerifySearchProjectionSessionTier(ctx context.Context, generationID string) error {
	if strings.TrimSpace(generationID) == "" {
		return &apptypes.SearchProjectionNoProgressError{Reason: "session tier verification requires a generation id"}
	}
	db, err := d.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return verifySearchProjectionSessionTier(ctx, db, generationID)
}

func verifySearchProjectionSessionTier(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, generationID string) error {
	var sessionID, summary string
	err := q.QueryRowContext(ctx, `
		SELECT sum.session_id, sum.summary_text
		  FROM search_projection_session_summaries sum
		  JOIN sessions s ON s.session_id = sum.session_id
		 WHERE sum.generation_id = ?
		   AND length(trim(sum.summary_text)) >= 3
		 ORDER BY sum.session_id
		 LIMIT 1`,
		generationID,
	).Scan(&sessionID, &summary)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No joinable session summary: either the corpus is empty or only
			// orphan events exist. Session tier has nothing to answer; do not
			// block reclaim of an empty previous generation.
			return nil
		}
		return xerrors.Errorf("select session tier verification candidate: %w", err)
	}
	term := sessionTierVerificationTerm(summary)
	if term == "" {
		return &apptypes.SearchProjectionNoProgressError{Reason: "session tier verification could not derive a query term"}
	}
	// Same contract as queryProjectionSessionHits: keyword equality or summary
	// LIKE, pinned to the generation under construction and scoped to the
	// probed session. The scope matters: sessions in one workspace share
	// vocabulary, so a term drawn from one summary routinely matches others.
	// The question this gate asks is whether the tier can answer for a session
	// it holds a summary for — not whether that session outranks its peers.
	// Ranking is a search concern, and demanding the top slot here would fail
	// every store whose summaries share a word.
	keyword := foldSearchASCII(term)
	likeQuery := "%" + escapeLikeQuery(term) + "%"
	var hitSession string
	err = q.QueryRowContext(ctx, `
		SELECT sum.session_id
		  FROM search_projection_session_summaries sum
		  JOIN sessions s ON s.session_id = sum.session_id
		 WHERE sum.generation_id = ?
		   AND sum.session_id = ?
		   AND (
		         EXISTS (
		           SELECT 1
		             FROM search_projection_session_keywords k
		            WHERE k.generation_id = sum.generation_id
		              AND k.session_id = sum.session_id
		              AND k.keyword = ?
		         )
		         OR sum.summary_text LIKE ? ESCAPE '\'
		       )
		 ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
		 LIMIT 1`,
		generationID, sessionID, keyword, likeQuery,
	).Scan(&hitSession)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &apptypes.SearchProjectionNoProgressError{
				Reason: "session tier verification query returned no hits for generation " + generationID,
			}
		}
		return xerrors.Errorf("run session tier verification query: %w", err)
	}
	if hitSession != sessionID {
		return &apptypes.SearchProjectionNoProgressError{
			Reason: "session tier verification returned unexpected session " + hitSession,
		}
	}
	return nil
}

// sessionTierVerificationTerm picks a stable ≥3-rune token from a summary so
// the verification query exercises the same LIKE path production search uses.
func sessionTierVerificationTerm(summary string) string {
	fields := strings.Fields(summary)
	for _, field := range fields {
		runes := []rune(strings.TrimSpace(field))
		if len(runes) >= 3 {
			return string(runes)
		}
	}
	runes := []rune(strings.TrimSpace(summary))
	if len(runes) < 3 {
		return ""
	}
	if len(runes) > 32 {
		runes = runes[:32]
	}
	return string(runes)
}

func projectionCutoff(timestamp time.Time) string {
	return timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

//nolint:wrapcheck,errcheck // SQL errors remain inside the SQLite adapter; rollback is best effort.
func markProjectionDrifted(ctx context.Context, db *sql.DB, generation string) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE search_projection_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, generation)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return &apptypes.SearchProjectionNoProgressError{Reason: "generation state changed concurrently"}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE search_projection_inventory_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, generation); e != nil {
		return e
	}
	r, e = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=? AND state='rebuilding'`, generation)
	if e != nil {
		return e
	}
	n, e = r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return &apptypes.SearchProjectionNoProgressError{Reason: "generation lifecycle changed concurrently"}
	}
	return tx.Commit()
}
