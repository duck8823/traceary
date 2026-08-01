package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CapacityBenchmarkQuery is a production query and sanitized binding set used by capacity evidence.
type CapacityBenchmarkQuery struct {
	Name string
	SQL  string
	Args []any
}

// CapacityBenchmarkQueries returns the same SQL sources used by production active/latest/handoff/search reads.
func CapacityBenchmarkQueries(ctx context.Context, db *sql.DB) ([]CapacityBenchmarkQuery, error) {
	searchCriteria := apptypes.NewEventSearchCriteriaBuilder(20).Query("synthetic-needle").Build()
	complete, err := eventSearchBackfillComplete(ctx, db)
	if err != nil {
		return nil, xerrors.Errorf("inspect production search orchestration: %w", err)
	}
	searchSQL, searchArgs := buildFTSEventIDsQuery(searchCriteria, searchCriteria.Query())
	if !complete {
		searchSQL, searchArgs = buildIncompleteFTSEventIDsQuery(searchCriteria, searchCriteria.Query())
	}
	latestSQL := latestSessionBoundarySQL(ctx, db)
	return []CapacityBenchmarkQuery{
		{Name: "active", SQL: findActiveSessionQuery, Args: []any{"session_started", "", "", "", "", "", "", "session_started", "session_ended"}},
		{Name: "latest", SQL: latestSQL, Args: []any{"session_started", "", "", "", "", "", "", "session_started"}},
		{Name: "search", SQL: searchSQL, Args: searchArgs},
	}, nil
}

// CapacityHandoffPlanQueries returns fixed SQL constituents selected by the production ContextPackBuilder path.
func CapacityHandoffPlanQueries() []CapacityBenchmarkQuery {
	return []CapacityBenchmarkQuery{
		{Name: "session_resolution", SQL: listSessionsQuery, Args: []any{"", "", "", "", "", "", "", "", "", "", "", "", false, "", "", "", "", 1, 0}},
		{Name: "recent_command_previews", SQL: selectRecentCommandPreviewsQuery, Args: []any{"", "", 5, 512}},
		{Name: "compact_summary", SQL: selectLatestPostCompactSummaryQuery, Args: []any{"", "", ""}},
	}
}
