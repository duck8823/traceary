package sqlite

import (
	"context"
	"database/sql"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const searchProjectionVersion = 1

var searchProjectionToken = regexp.MustCompile(`[[:alnum:]_./:@+-]+`)

// RebuildSearchProjections runs one explicitly requested bounded batch.
func (d *Database) RebuildSearchProjections(ctx context.Context, budget apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	db, err := d.open(ctx)
	if err != nil {
		return apptypes.SearchProjectionProgress{}, xerrors.Errorf("open search projection store: %w", err)
	}
	defer func() { _ = db.Close() }()
	return rebuildSearchProjections(ctx, db, budget, now)
}

// SearchProjectionStatus returns only version/provenance/capacity counters.
func (d *Database) SearchProjectionStatus(ctx context.Context) (apptypes.SearchProjectionStatus, error) {
	db, err := d.openReadOnly(ctx)
	if err != nil {
		return apptypes.SearchProjectionStatus{}, xerrors.Errorf("open search projection status: %w", err)
	}
	defer func() { _ = db.Close() }()
	var s apptypes.SearchProjectionStatus
	var complete int
	err = db.QueryRowContext(ctx, `SELECT projection_version,fts_design,completed,recent_age_seconds,recent_byte_limit,recent_bytes,recent_documents,summary_sessions,keyword_rows FROM search_projection_state WHERE singleton=1`).Scan(&s.ProjectionVersion, &s.FTSDesign, &complete, &s.RecentAgeSeconds, &s.RecentByteLimit, &s.RecentBytes, &s.RecentDocuments, &s.SummarySessions, &s.KeywordRows)
	s.Completed = complete != 0
	if err != nil {
		return apptypes.SearchProjectionStatus{}, xerrors.Errorf("read search projection status: %w", err)
	}
	return s, nil
}

// rebuildSearchProjections advances one atomic, bounded prefix. It is derived
// data only: no legacy search table or canonical payload is modified.
func rebuildSearchProjections(ctx context.Context, db *sql.DB, budget apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionProgress, error) {
	var out apptypes.SearchProjectionProgress
	if !budget.Valid() {
		return out, xerrors.Errorf("search projection budgets must all be positive")
	}
	deadline := time.Now().Add(budget.WallTime)
	lockDeadline := time.Now().Add(budget.LockTime)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, xerrors.Errorf("begin search projection rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var last string
	var target sql.NullString
	var completed int
	if err := tx.QueryRowContext(ctx, `SELECT last_event_id,target_event_id,completed FROM search_projection_state WHERE singleton=1`).Scan(&last, &target, &completed); err != nil {
		return out, xerrors.Errorf("read search projection checkpoint: %w", err)
	}
	if completed != 0 {
		out.Completed = true
		if err := tx.Commit(); err != nil {
			return out, xerrors.Errorf("commit completed search projection check: %w", err)
		}
		return out, nil
	}
	if !target.Valid {
		// A NULL target denotes a new generation, not a resume. Replace every
		// derived row before freezing the new source prefix so an explicit
		// rebuild is idempotent rather than incrementing prior aggregates.
		for _, table := range []string{"search_projection_recent_documents", "search_projection_session_summaries", "search_projection_session_keywords"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
				return out, xerrors.Errorf("reset search projection generation: %w", err)
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT MAX(id) FROM events`).Scan(&target); err != nil {
			return out, xerrors.Errorf("freeze search projection target: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE search_projection_state SET target_event_id=? WHERE singleton=1`, target); err != nil {
			return out, xerrors.Errorf("persist search projection target: %w", err)
		}
	}
	if !target.Valid {
		out.Completed = true
		_, err = tx.ExecContext(ctx, `UPDATE search_projection_state SET completed=1,updated_at=? WHERE singleton=1`, formatTimestamp(now))
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return out, xerrors.Errorf("finish empty search projection: %w", err)
		}
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,session_id,created_at,kind FROM events WHERE id>? AND id<=? ORDER BY id LIMIT ?`, last, target.String, budget.Rows)
	if err != nil {
		return out, xerrors.Errorf("select search projection batch: %w", err)
	}
	type source struct{ id, session, created, kind string }
	batch := make([]source, 0, budget.Rows)
	for rows.Next() {
		var s source
		if err := rows.Scan(&s.id, &s.session, &s.created, &s.kind); err != nil {
			_ = rows.Close()
			return out, xerrors.Errorf("scan search projection source: %w", err)
		}
		batch = append(batch, s)
	}
	if err := rows.Close(); err != nil {
		return out, xerrors.Errorf("close search projection source: %w", err)
	}
	for _, s := range batch {
		if time.Now().After(deadline) || time.Now().After(lockDeadline) {
			break
		}
		plain, e := loadEventPlaintext(ctx, tx, s.id)
		if e != nil {
			return out, xerrors.Errorf("hydrate search projection event body: %w", e)
		}
		visible, _ := visibleEventBody(string(plain), types.BodyAvailabilityAvailable)
		command, e := hydrateAuditPayload(ctx, tx, s.id, "command")
		if e != nil {
			return out, xerrors.Errorf("measure recent search projection bytes: %w", e)
		}
		input, e := hydrateAuditPayload(ctx, tx, s.id, "input")
		if e != nil {
			return out, e
		}
		output, e := hydrateAuditPayload(ctx, tx, s.id, "output")
		if e != nil {
			return out, e
		}
		canonical := strings.Join([]string{visible, command.String, input.String, output.String}, "\n")
		decoded := int64(len(plain) + len(command.String) + len(input.String) + len(output.String))
		// Hydration has already enforced the physical stored-byte ceiling per
		// field. The available encoded length metadata is conservatively
		// represented by the materialized bytes for identity rows; compressed
		// rows remain bounded by the same central hydration boundary.
		stored := decoded
		written := int64(len(canonical))
		if stored > budget.StoredBytes-out.StoredBytes || decoded > budget.DecodedBytes-out.DecodedBytes || written > budget.WriteBytes-out.WrittenBytes {
			break
		}
		if created, e := time.Parse(time.RFC3339Nano, s.created); e == nil && !created.Before(now.Add(-budget.RecentAge)) {
			if _, e = tx.ExecContext(ctx, `INSERT INTO search_projection_recent_documents(event_id,created_at,body_text,decoded_bytes) VALUES(?,?,lower(?),?) ON CONFLICT(event_id) DO UPDATE SET created_at=excluded.created_at,body_text=excluded.body_text,decoded_bytes=excluded.decoded_bytes`, s.id, s.created, canonical, written); e != nil {
				return out, xerrors.Errorf("write recent search document: %w", e)
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO search_projection_session_summaries(session_id,event_count,command_count,failure_count,summary_text,projection_version) VALUES(?,1,CASE WHEN ?<>'' THEN 1 ELSE 0 END,CASE WHEN EXISTS(SELECT 1 FROM command_audits WHERE event_id=? AND failed=1) THEN 1 ELSE 0 END,?,?) ON CONFLICT(session_id) DO UPDATE SET event_count=event_count+1,command_count=command_count+excluded.command_count,failure_count=failure_count+excluded.failure_count,summary_text=summary_text||' '||excluded.summary_text,projection_version=excluded.projection_version`, s.session, command.String, s.id, s.kind, searchProjectionVersion); e != nil {
			return out, xerrors.Errorf("write session search summary: %w", e)
		}
		counts := map[string]int{}
		for _, token := range searchProjectionToken.FindAllString(strings.ToLower(canonical), -1) {
			if highEntropyKeyword(token) {
				counts[token]++
			}
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, e = tx.ExecContext(ctx, `INSERT INTO search_projection_session_keywords(session_id,keyword,occurrences) VALUES(?,?,?) ON CONFLICT(session_id,keyword) DO UPDATE SET occurrences=occurrences+excluded.occurrences`, s.session, k, counts[k]); e != nil {
				return out, xerrors.Errorf("write session search keyword: %w", e)
			}
		}
		out.Selected++
		out.Written++
		out.StoredBytes += stored
		out.DecodedBytes += decoded
		out.WrittenBytes += written
		last = s.id
	}
	cutoff := formatTimestamp(now.Add(-budget.RecentAge))
	res, e := tx.ExecContext(ctx, `DELETE FROM search_projection_recent_documents WHERE created_at<?`, cutoff)
	if e != nil {
		return out, xerrors.Errorf("evict aged recent search documents: %w", e)
	}
	n, _ := res.RowsAffected()
	out.Evicted += int(n)
	for {
		var total int64
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents`).Scan(&total); e != nil {
			return out, xerrors.Errorf("measure recent search projection bytes: %w", e)
		}
		if total <= budget.RecentBytes {
			break
		}
		res, e = tx.ExecContext(ctx, `DELETE FROM search_projection_recent_documents WHERE event_id=(SELECT event_id FROM search_projection_recent_documents ORDER BY created_at,event_id LIMIT 1)`)
		if e != nil {
			return out, xerrors.Errorf("evict recent search byte budget: %w", e)
		}
		n, _ = res.RowsAffected()
		if n == 0 {
			break
		}
		out.Evicted += int(n)
	}
	out.Completed = last == target.String
	if _, e = tx.ExecContext(ctx, `UPDATE search_projection_state SET last_event_id=?,completed=?,recent_age_seconds=?,recent_byte_limit=?,recent_bytes=(SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents),recent_documents=(SELECT COUNT(*) FROM search_projection_recent_documents),summary_sessions=(SELECT COUNT(*) FROM search_projection_session_summaries),keyword_rows=(SELECT COUNT(*) FROM search_projection_session_keywords),updated_at=? WHERE singleton=1`, last, boolToInt(out.Completed), int64(budget.RecentAge/time.Second), budget.RecentBytes, formatTimestamp(now)); e != nil {
		return out, xerrors.Errorf("checkpoint search projection rebuild: %w", e)
	}
	if e = tx.Commit(); e != nil {
		return out, xerrors.Errorf("commit search projection rebuild: %w", e)
	}
	return out, nil
}

func highEntropyKeyword(s string) bool {
	if utf8.RuneCountInString(s) < 12 {
		return false
	}
	var alpha, digit, symbol bool
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			alpha = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	return alpha && (digit || symbol)
}
