package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	apptypes "github.com/duck8823/traceary/application/types"
)

const searchProjectionVersion = 1
const searchProjectionKeywordVersion = 1

var searchProjectionToken = regexp.MustCompile(`[[:alnum:]_./:@+-]+`)

func generationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

// StartSearchProjectionGeneration freezes monotonic SQLite rowid membership.
// Inserts after this point belong to the next generation; updates/deletes cause drift.
//
//nolint:wrapcheck,errcheck // SQL errors are returned without losing typed projection errors.
func (d *Database) StartSearchProjectionGeneration(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if !b.Valid() {
		return apptypes.SearchProjectionGeneration{}, errors.New("search projection budgets must all be positive")
	}
	db, e := d.open(ctx)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer tx.Rollback()
	g := apptypes.SearchProjectionGeneration{GenerationID: generationID(), ConfigHash: b.ConfigHash()}
	if e = tx.QueryRowContext(ctx, `SELECT revision,(SELECT COALESCE(MAX(rowid),0) FROM events) FROM search_projection_source_revision WHERE singleton=1`).Scan(&g.SourceRevision, &g.HighWater); e != nil {
		return g, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE search_projection_state SET generation_id=?,config_hash=?,source_revision=?,high_water=?,checkpoint=0,state='rebuilding',recent_age_seconds=?,recent_byte_limit=?,updated_at=? WHERE singleton=1`, g.GenerationID, g.ConfigHash, g.SourceRevision, g.HighWater, int64(b.RecentAge/time.Second), b.RecentBytes, formatTimestamp(now.UTC()))
	if e == nil {
		e = tx.Commit()
	}
	return g, e
}

// RebuildSearchProjections resumes one bounded generation batch.
//
//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func (d *Database) RebuildSearchProjections(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	db, e := d.open(ctx)
	if e != nil {
		return apptypes.SearchProjectionProgress{}, e
	}
	defer db.Close()
	return rebuildSearchProjections(ctx, db, b, now)
}

type projectionSource struct {
	rowid                                    int64
	id, session, created, availability, kind string
	stored, decoded                          int64
}
type projectionDoc struct {
	projectionSource
	text, summary              string
	commandCount, failureCount int
	keywords                   map[string]int
	write                      int64
}

//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func rebuildSearchProjections(ctx context.Context, db *sql.DB, b apptypes.SearchProjectionBudget, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	if !b.Valid() {
		return out, errors.New("search projection budgets must all be positive")
	}
	started := time.Now()
	var g apptypes.SearchProjectionGeneration
	var state string
	if err = db.QueryRowContext(ctx, `SELECT generation_id,config_hash,source_revision,high_water,checkpoint,state FROM search_projection_state WHERE singleton=1`).Scan(&g.GenerationID, &g.ConfigHash, &g.SourceRevision, &g.HighWater, &g.Checkpoint, &state); err != nil {
		return out, err
	}
	out.GenerationID = g.GenerationID
	if state == "idle" || g.GenerationID == "" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "no generation has been started"}
	}
	if g.ConfigHash != b.ConfigHash() {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&revision); err != nil {
		return out, err
	}
	if revision != g.SourceRevision {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	// Selection is bounded before payload materialization. Stored and decoded metadata reserve every payload field.
	rows, e := db.QueryContext(ctx, `SELECT e.rowid,e.id,e.session_id,e.created_at_norm,e.body_availability,e.kind,
	 COALESCE(length(CAST(e.body AS BLOB)),0)+COALESCE(length(CAST(a.command_text AS BLOB)),0)+COALESCE(length(CAST(a.input_text AS BLOB)),0)+COALESCE(length(CAST(a.output_text AS BLOB)),0),
	 CASE WHEN e.body_availability='available' THEN COALESCE(e.body_plaintext_bytes,e.body_stored_bytes,length(CAST(e.body AS BLOB)),0) ELSE 0 END+COALESCE(a.command_plaintext_bytes,length(CAST(a.command_text AS BLOB)),0)+COALESCE(a.input_plaintext_bytes,length(CAST(a.input_text AS BLOB)),0)+COALESCE(a.output_plaintext_bytes,length(CAST(a.output_text AS BLOB)),0)
	 FROM events e LEFT JOIN command_audits a ON a.event_id=e.id WHERE e.rowid>? AND e.rowid<=? ORDER BY e.rowid LIMIT ?`, g.Checkpoint, g.HighWater, b.Rows)
	if e != nil {
		return out, e
	}
	var src []projectionSource
	for rows.Next() {
		var s projectionSource
		if e = rows.Scan(&s.rowid, &s.id, &s.session, &s.created, &s.availability, &s.kind, &s.stored, &s.decoded); e != nil {
			rows.Close()
			return out, e
		}
		if s.stored > b.StoredBytes {
			return out, &apptypes.SearchProjectionOversizeError{Class: "stored_bytes", Bytes: s.stored, Limit: b.StoredBytes}
		}
		if s.decoded > b.DecodedBytes {
			return out, &apptypes.SearchProjectionOversizeError{Class: "decoded_bytes", Bytes: s.decoded, Limit: b.DecodedBytes}
		}
		if out.StoredBytes+s.stored > b.StoredBytes || out.DecodedBytes+s.decoded > b.DecodedBytes {
			break
		}
		src = append(src, s)
		out.StoredBytes += s.stored
		out.DecodedBytes += s.decoded
	}
	rows.Close()
	deadline := started.Add(b.WallTime)
	docs := make([]projectionDoc, 0, len(src))
	for _, s := range src {
		if time.Now().After(deadline) {
			break
		}
		p := projectionDoc{projectionSource: s, keywords: map[string]int{}}
		var body string
		if s.availability == "available" {
			plain, x := loadEventPlaintext(ctx, db, s.id)
			if x != nil {
				return out, x
			}
			body = string(plain)
		}
		cmd, x := hydrateAuditPayload(ctx, db, s.id, "command")
		if x != nil {
			return out, x
		}
		in, x := hydrateAuditPayload(ctx, db, s.id, "input")
		if x != nil {
			return out, x
		}
		output, x := hydrateAuditPayload(ctx, db, s.id, "output")
		if x != nil {
			return out, x
		}
		parts := make([]string, 0, 4)
		for _, v := range []string{body, cmd.String, in.String, output.String} {
			if v != "" {
				parts = append(parts, v)
			}
		}
		p.text = strings.Join(parts, "\n")
		p.summary = body
		p.write = int64(len(p.text) + len(p.summary))
		if cmd.Valid && cmd.String != "" {
			p.commandCount = 1
		}
		_ = db.QueryRowContext(ctx, `SELECT COALESCE(failed,0) FROM command_audits WHERE event_id=?`, s.id).Scan(&p.failureCount)
		for _, token := range searchProjectionToken.FindAllString(strings.ToLower(p.text), -1) {
			if highEntropyKeyword(token) {
				p.keywords[token]++
			}
		}
		// Include aggregate/index amplification in the write reservation.
		p.write += int64(len(p.text) + len(s.session))
		for k := range p.keywords {
			p.write += int64(len(k) + 24)
		}
		if p.write > b.WriteBytes {
			return out, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: p.write, Limit: b.WriteBytes}
		}
		if out.WrittenBytes+p.write > b.WriteBytes {
			break
		}
		out.WrittenBytes += p.write
		docs = append(docs, p)
	}
	if len(src) > 0 && len(docs) == 0 {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "wall or write budget prevented the first row"}
	}
	lockCtx, cancel := context.WithTimeout(ctx, b.LockTime)
	defer cancel()
	tx, e := db.BeginTx(lockCtx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var current int64
	if e = tx.QueryRowContext(lockCtx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&current); e != nil {
		return out, e
	}
	if current != g.SourceRevision {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	last := g.Checkpoint
	for _, p := range docs {
		if p.created != "" && p.created >= formatTimestamp(now.UTC().Add(-b.RecentAge)) {
			_, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, g.GenerationID, p.rowid, p.id, p.created, p.text, len(p.text))
			if e != nil {
				return out, e
			}
		}
		_, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_session_summaries VALUES(?,?,1,?,?) ON CONFLICT(generation_id,session_id) DO UPDATE SET event_count=event_count+1,summary_text=trim(summary_text||char(10)||excluded.summary_text)`, g.GenerationID, p.session, p.summary, searchProjectionVersion)
		if e != nil {
			return out, e
		}
		_, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_command_aggregates VALUES(?,?,?,?) ON CONFLICT(generation_id,session_id) DO UPDATE SET command_count=command_count+excluded.command_count,failure_count=failure_count+excluded.failure_count`, g.GenerationID, p.session, p.commandCount, p.failureCount)
		if e != nil {
			return out, e
		}
		keys := make([]string, 0, len(p.keywords))
		for k := range p.keywords {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, e = tx.ExecContext(lockCtx, `INSERT INTO search_projection_session_keywords VALUES(?,?,?,?,?) ON CONFLICT(generation_id,session_id,keyword) DO UPDATE SET occurrences=occurrences+excluded.occurrences`, g.GenerationID, p.session, k, p.keywords[k], searchProjectionKeywordVersion); e != nil {
				return out, e
			}
		}
		last = p.rowid
		out.Selected++
		out.Written++
	}
	res, e := tx.ExecContext(lockCtx, `DELETE FROM search_projection_recent_documents WHERE rowid IN (SELECT rowid FROM search_projection_recent_documents WHERE generation_id=? AND (created_at_norm<? OR (SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents WHERE generation_id=?)>?) ORDER BY created_at_norm,event_rowid LIMIT ?)`, g.GenerationID, formatTimestamp(now.UTC().Add(-b.RecentAge)), g.GenerationID, b.RecentBytes, b.Rows)
	if e != nil {
		return out, e
	}
	n, _ := res.RowsAffected()
	out.Evicted = int(n)
	var retained int64
	_ = tx.QueryRowContext(lockCtx, `SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents WHERE generation_id=?`, g.GenerationID).Scan(&retained)
	out.Completed = last == g.HighWater && retained <= b.RecentBytes
	_, e = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET checkpoint=?,state=?,last_batch_milliseconds=?,updated_at=? WHERE singleton=1 AND generation_id=? AND source_revision=?`, last, map[bool]string{true: "complete", false: "rebuilding"}[out.Completed], time.Since(started).Milliseconds(), formatTimestamp(now.UTC()), g.GenerationID, g.SourceRevision)
	if e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return out, nil
}

// SearchProjectionStatus returns payload-free operational evidence.
//
//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func (d *Database) SearchProjectionStatus(ctx context.Context) (s apptypes.SearchProjectionStatus, err error) {
	db, e := d.openReadOnly(ctx)
	if e != nil {
		return s, e
	}
	defer db.Close()
	s.SchemaVersion = "traceary.search-projection-status/v1"
	s.KeywordVersion = searchProjectionKeywordVersion
	e = db.QueryRowContext(ctx, `SELECT state,projection_version,fts_design,config_hash,source_revision,high_water,checkpoint,state='complete',recent_age_seconds,recent_byte_limit,last_batch_milliseconds FROM search_projection_state WHERE singleton=1`).Scan(&s.State, &s.ProjectionVersion, &s.FTSDesign, &s.ConfigHash, &s.SourceRevision, &s.HighWater, &s.Checkpoint, &s.Completed, &s.RecentAgeSeconds, &s.RecentByteLimit, &s.LastBatchMilliseconds)
	if e != nil {
		return s, e
	}
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(decoded_bytes),0),COUNT(*) FROM search_projection_recent_documents WHERE generation_id=(SELECT generation_id FROM search_projection_state)`).Scan(&s.RecentBytes, &s.RecentDocuments)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(summary_text AS BLOB))),0) FROM search_projection_session_summaries WHERE generation_id=(SELECT generation_id FROM search_projection_state)`).Scan(&s.SummarySessions, &s.SummaryLogicalBytes)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(keyword AS BLOB))),0) FROM search_projection_session_keywords WHERE generation_id=(SELECT generation_id FROM search_projection_state)`).Scan(&s.KeywordRows, &s.KeywordLogicalBytes)
	s.FTSLogicalBytes = s.RecentBytes
	var page int64
	if db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&page) == nil {
		if e = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name LIKE 'search_projection_%'`).Scan(&s.PhysicalBytes); e == nil {
			s.PhysicalEvidence = apptypes.CapacityEvidence{Status: "complete", Method: "dbstat"}
		} else {
			s.PhysicalEvidence = apptypes.CapacityEvidence{Status: "unavailable", Method: "pragma", Reason: "dbstat unavailable"}
			e = nil
		}
	}
	return s, e
}

func highEntropyKeyword(s string) bool {
	if utf8.RuneCountInString(s) < 12 {
		return false
	}
	var a, d, y bool
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			a = true
		case r >= '0' && r <= '9':
			d = true
		default:
			y = true
		}
	}
	return a && (d || y)
}
