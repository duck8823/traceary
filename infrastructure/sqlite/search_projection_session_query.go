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

// Search and readiness are asserted together: MCP reports why a session group
// is empty, and losing readiness here would make that report silently claim
// "ready" instead of failing to build.
var _ queryservice.ProjectionSessionSearch = (*EventDatasource)(nil)

// SearchSessionPage returns session-tier matches and the readiness that
// explains an empty page from one read-only transaction. Early exits
// (empty query, --kind, later pages) never open the store and report
// not_applicable so callers do not invent a readiness they did not observe.
func (d *EventDatasource) SearchSessionPage(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
	excludeSessionIDs []types.SessionID,
) (apptypes.SearchSessionPage, error) {
	if criteria.Limit() <= 0 {
		return apptypes.SearchSessionPage{}, xerrors.New("limit must be greater than or equal to 1")
	}
	queryValue := strings.TrimSpace(criteria.Query())
	if queryValue == "" || strings.TrimSpace(criteria.Kind().String()) != "" || criteria.Offset() > 0 || !criteria.PageAnchor().IsZero() {
		return apptypes.SearchSessionPageOf(nil, apptypes.SearchSessionTierNotApplicable), nil
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.SearchSessionPage{}, xerrors.Errorf("failed to open DB for projection session search: %w", err)
	}
	defer d.db.release(db)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return apptypes.SearchSessionPage{}, xerrors.Errorf("failed to begin projection session search: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	ready, err := searchProjectionReadReady(ctx, tx)
	if err != nil {
		return apptypes.SearchSessionPage{}, err
	}
	if !ready {
		if commitErr := tx.Commit(); commitErr != nil {
			return apptypes.SearchSessionPage{}, xerrors.Errorf("failed to finish empty projection session search: %w", commitErr)
		}
		return apptypes.SearchSessionPageOf(nil, apptypes.SearchSessionTierNotReady), nil
	}

	hits, err := queryProjectionSessionHits(ctx, tx, criteria, queryValue, excludeSessionIDs)
	if err != nil {
		return apptypes.SearchSessionPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return apptypes.SearchSessionPage{}, xerrors.Errorf("failed to finish projection session search: %w", err)
	}
	return apptypes.SearchSessionPageOf(hits, apptypes.SearchSessionTierReady), nil
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
	// Exact keyword + LIKE is the shipped session-tier match (#1756).
	// A porter FTS was measured and not added: LIKE already matches
	// stemming-as-substring, and the index would charge the family budget.
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
