package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domaintypes "github.com/duck8823/traceary/domain/types"
)

const searchProjectionVersion = 1
const searchProjectionKeywordVersion = 1
const searchProjectionSummaryVersion = 1

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
	lockCtx, cancel := context.WithTimeout(ctx, b.LockTime)
	defer cancel()
	tx, e := db.BeginTx(lockCtx, nil)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer tx.Rollback()
	g := apptypes.SearchProjectionGeneration{GenerationID: generationID(), ConfigHash: b.ConfigHash()}
	if e = tx.QueryRowContext(lockCtx, `SELECT revision,(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) FROM search_projection_source_revision WHERE singleton=1`).Scan(&g.SourceRevision, &g.HighWater); e != nil {
		return g, e
	}
	result, e := tx.ExecContext(lockCtx, `UPDATE search_projection_state SET generation_id=?,config_hash=?,source_revision=?,high_water=?,checkpoint=0,phase='source',cleanup_scope='old',failure_class='',state='rebuilding',recent_age_seconds=?,recent_byte_limit=?,updated_at=? WHERE singleton=1 AND state<>'rebuilding'`, g.GenerationID, g.ConfigHash, g.SourceRevision, g.HighWater, int64(b.RecentAge/time.Second), b.RecentBytes, formatTimestamp(now.UTC()))
	if e == nil {
		if n, x := result.RowsAffected(); x != nil || n != 1 {
			return g, &apptypes.SearchProjectionNoProgressError{Reason: "a generation is already rebuilding"}
		}
		if _, x := tx.ExecContext(lockCtx, `UPDATE literal_search_projection_state SET generation_id=?,high_water=?,fingerprint_version=1,state='rebuilding',updated_at=? WHERE singleton=1`, g.GenerationID, g.HighWater, formatTimestamp(now.UTC())); x != nil {
			return g, x
		}
		e = tx.Commit()
	}
	return g, e
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
	return d.applyProjectionPlan(ctx, p, lock, now)
}

// CleanupBatch applies only an application-planned eviction or old-generation batch.
func (d *Database) CleanupBatch(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	if p.Phase == "source" {
		return apptypes.SearchProjectionProgress{}, errors.New("cleanup batch requires cleanup phase")
	}
	return d.applyProjectionPlan(ctx, p, lock, now)
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
		if _, e = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE generation_id=? AND source_revision=?`, p.GenerationID, p.ExpectedRevision); e != nil {
			return out, e
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
				if _, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, p.GenerationID, d.Sequence, d.EventID, d.CreatedAt, d.Text, len(d.Text)); e != nil {
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
	out.Completed = p.Completed
	out.GenerationID = p.GenerationID
	out.StoredBytes = p.Ledger.StoredBytes
	out.DecodedBytes = p.Ledger.DecodedBytes
	out.WrittenBytes = p.Ledger.LogicalWriteBytes
	return out, nil
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
	e = db.QueryRowContext(ctx, `SELECT state,phase,projection_version,fts_design,config_hash,source_revision,high_water,checkpoint,state='complete',recent_age_seconds,recent_byte_limit,last_batch_milliseconds FROM search_projection_state WHERE singleton=1`).Scan(&s.State, &s.Phase, &s.ProjectionVersion, &s.FTSDesign, &s.ConfigHash, &s.SourceRevision, &s.HighWater, &s.Checkpoint, &s.Completed, &s.RecentAgeSeconds, &s.RecentByteLimit, &s.LastBatchMilliseconds)
	if e != nil {
		return s, e
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

func projectionCutoff(timestamp time.Time) string {
	return timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

//nolint:wrapcheck // SQL errors remain inside the SQLite adapter.
func markProjectionDrifted(ctx context.Context, db *sql.DB, generation string) error {
	r, e := db.ExecContext(ctx, `UPDATE search_projection_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, generation)
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
	return nil
}
