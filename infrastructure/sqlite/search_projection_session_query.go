package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// SearchSessionHits returns session-tier matches from the bounded search
// projection. When the projection is not complete, the method returns an empty
// page without error so callers fall through to event-only results.
func (d *EventDatasource) SearchSessionHits(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
	excludeSessionIDs []types.SessionID,
) ([]apptypes.SearchSessionHit, error) {
	if criteria.Limit() <= 0 {
		return nil, xerrors.New("limit must be greater than or equal to 1")
	}
	queryValue := strings.TrimSpace(criteria.Query())
	if queryValue == "" {
		return []apptypes.SearchSessionHit{}, nil
	}
	// Kind cannot be applied to session summaries/keywords. Fail closed rather
	// than return unfiltered session rows for a filter the user set.
	if strings.TrimSpace(criteria.Kind().String()) != "" {
		return []apptypes.SearchSessionHit{}, nil
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for projection session search: %w", err)
	}
	defer d.db.release(db)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, xerrors.Errorf("failed to begin projection session search: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	ready, err := searchProjectionReadReady(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !ready {
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, xerrors.Errorf("failed to finish empty projection session search: %w", commitErr)
		}
		return []apptypes.SearchSessionHit{}, nil
	}

	hits, err := queryProjectionSessionHits(ctx, tx, criteria, queryValue, excludeSessionIDs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish projection session search: %w", err)
	}
	return hits, nil
}

func queryProjectionSessionHits(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
	excludeSessionIDs []types.SessionID,
) ([]apptypes.SearchSessionHit, error) {
	var builder strings.Builder
	builder.WriteString(`
		SELECT sum.session_id, sum.summary_text, sum.event_count, s.started_at
		  FROM search_projection_session_summaries sum
		  JOIN sessions s ON s.session_id = sum.session_id
		 WHERE sum.generation_id = (
		         SELECT active_generation_id
		           FROM search_projection_state
		          WHERE singleton = 1
		       )
		   AND (
		         EXISTS (
		           SELECT 1
		             FROM search_projection_session_keywords k
		            WHERE k.generation_id = sum.generation_id
		              AND k.session_id = sum.session_id
		              AND k.keyword = ?
		         )
		         OR sum.summary_text LIKE ? ESCAPE '\'
		       )`)
	keyword := foldSearchASCII(queryValue)
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	args := []any{keyword, likeQuery}
	args = appendSessionSearchFilters(&builder, args, criteria, excludeSessionIDs)
	builder.WriteString(" ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC LIMIT ?")
	args = append(args, criteria.Limit())

	rows, err := queryer.QueryContext(ctx, builder.String(), args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query projection session hits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hits := make([]apptypes.SearchSessionHit, 0)
	for rows.Next() {
		var sessionID, summary, startedAtRaw string
		var eventCount int
		if err := rows.Scan(&sessionID, &summary, &eventCount, &startedAtRaw); err != nil {
			return nil, xerrors.Errorf("failed to scan projection session hit: %w", err)
		}
		startedAt, err := time.Parse(time.RFC3339Nano, startedAtRaw)
		if err != nil {
			startedAt, err = time.Parse(time.RFC3339, startedAtRaw)
			if err != nil {
				return nil, xerrors.Errorf("failed to parse session started_at %q: %w", startedAtRaw, err)
			}
		}
		hits = append(hits, apptypes.SearchSessionHitOf(
			types.SessionID(sessionID),
			summary,
			eventCount,
			startedAt.UTC(),
		))
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate projection session hits: %w", err)
	}
	return hits, nil
}

func appendSessionSearchFilters(
	builder *strings.Builder,
	args []any,
	criteria apptypes.EventSearchCriteria,
	excludeSessionIDs []types.SessionID,
) []any {
	if value := strings.TrimSpace(criteria.Workspace().String()); value != "" {
		builder.WriteString(" AND s.workspace = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.SessionID().String()); value != "" {
		builder.WriteString(" AND s.session_id = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.Client().String()); value != "" {
		builder.WriteString(" AND s.client = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.Agent().String()); value != "" {
		builder.WriteString(" AND s.agent = ?")
		args = append(args, value)
	}
	// sessions has no persisted _norm column, so every started_at comparison goes
	// through ts_norm on both sides. RFC3339Nano is variable width and '.' 0x2E
	// sorts below 'Z' 0x5A, so raw string comparison drops sub-second timestamps
	// out of range (#1185).
	if !criteria.From().IsZero() {
		builder.WriteString(" AND ts_norm(s.started_at) >= ts_norm(?)")
		args = append(args, formatTimestamp(criteria.From()))
	}
	if !criteria.To().IsZero() {
		builder.WriteString(" AND ts_norm(s.started_at) < ts_norm(?)")
		args = append(args, formatTimestamp(criteria.To()))
	}
	if criteria.FailuresOnly() {
		// Session tier can honour failures through command aggregates only.
		builder.WriteString(`
		   AND EXISTS (
		         SELECT 1
		           FROM search_projection_command_aggregates a
		          WHERE a.generation_id = sum.generation_id
		            AND a.session_id = sum.session_id
		            AND a.failure_count > 0
		       )`)
	}
	if len(excludeSessionIDs) > 0 {
		builder.WriteString(" AND s.session_id NOT IN (")
		for i, sessionID := range excludeSessionIDs {
			if i > 0 {
				builder.WriteByte(',')
			}
			builder.WriteByte('?')
			args = append(args, sessionID.String())
		}
		builder.WriteByte(')')
	}
	return args
}

var _ queryservice.ProjectionSessionSearchQuery = (*EventDatasource)(nil)
