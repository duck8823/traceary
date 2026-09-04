package sqlite

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

func buildTwoTierRefinementQuery(
	criteria apptypes.EventSearchCriteria,
	phrase apptypes.SearchPhrase,
) (string, []any) {
	var builder strings.Builder
	builder.WriteString(`
SELECT r.session_id, r.summary, r.produced_at, s.started_at
  FROM session_refinements r
  JOIN sessions s ON s.session_id = r.session_id
 WHERE r.summary LIKE ? ESCAPE '\'`)
	args := []any{"%" + escapeLikeQuery(phrase.Canonical()) + "%"}
	args = appendTwoTierSessionFilters(&builder, args, criteria)
	return builder.String(), args
}

func appendTwoTierSessionFilters(
	builder *strings.Builder,
	args []any,
	criteria apptypes.EventSearchCriteria,
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
	if !criteria.From().IsZero() {
		builder.WriteString(" AND s.started_at_norm >= ?")
		args = append(args, normalizeRFC3339NanoForCompare(formatTimestamp(criteria.From())))
	}
	if !criteria.To().IsZero() {
		builder.WriteString(" AND s.started_at_norm < ?")
		args = append(args, normalizeRFC3339NanoForCompare(formatTimestamp(criteria.To())))
	}
	return args
}

func queryTwoTierRefinementHits(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	phrase apptypes.SearchPhrase,
) ([]twoTierHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("two-tier refinement scan cancelled: %w", err)
	}
	sqlText, args := buildTwoTierRefinementQuery(twoTierFilterCriteria(criteria), phrase)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, xerrors.Errorf("query two-tier refinement hits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hits := make([]twoTierHit, 0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, xerrors.Errorf("two-tier refinement scan cancelled: %w", err)
		}
		var sessionID, summary, producedAtRaw, startedAtRaw string
		if err := rows.Scan(&sessionID, &summary, &producedAtRaw, &startedAtRaw); err != nil {
			return nil, xerrors.Errorf("scan two-tier refinement hit: %w", err)
		}
		if !phrase.Matches(summary) {
			continue
		}
		producedAt, err := parseTwoTierTime(producedAtRaw, "session_refinements.produced_at")
		if err != nil {
			return nil, err
		}
		startedAt, err := parseTwoTierTime(startedAtRaw, "sessions.started_at")
		if err != nil {
			return nil, err
		}
		hits = append(hits, twoTierHit{
			resultTime: producedAt,
			tier:       apptypes.SearchHitTierRefinement,
			kind:       twoTierResultKindSession,
			sessionID:  sessionID,
			session: apptypes.SearchSessionHitOf(
				types.SessionID(sessionID),
				summary,
				0,
				startedAt,
			).WithTier(apptypes.SearchHitTierRefinement),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate two-tier refinement hits: %w", err)
	}
	return hits, nil
}

func parseTwoTierTime(raw, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, xerrors.Errorf("failed to parse %s %q: %w", field, raw, err)
		}
	}
	return parsed.UTC(), nil
}
