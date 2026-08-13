package sqlite

import (
	"context"
	_ "embed"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/list_session_wake_summaries.sql
var listSessionWakeSummariesQuery string

// SessionWakeSummaryDatasource loads finished session summaries for wake injection.
type SessionWakeSummaryDatasource struct {
	db *Database
}

// NewSessionWakeSummaryDatasource creates a datasource bound to db.
func NewSessionWakeSummaryDatasource(db *Database) *SessionWakeSummaryDatasource {
	return &SessionWakeSummaryDatasource{db: db}
}

var _ queryservice.SessionWakeSummaryQueryService = (*SessionWakeSummaryDatasource)(nil)

// ListEligible returns top-level refinements that have agent reasoning.
// When session_refinements is absent (pre-migration-46 store), returns empty.
func (d *SessionWakeSummaryDatasource) ListEligible(
	ctx context.Context,
	workspace types.Workspace,
	excludeSessionID types.SessionID,
	limit int,
) ([]queryservice.SessionWakeSummary, error) {
	if d == nil || d.db == nil {
		return nil, xerrors.Errorf("session wake summary datasource is not configured")
	}
	if limit <= 0 {
		return nil, nil
	}

	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open read-only DB for wake summaries: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	exists, err := databaseTableExists(ctx, db, "session_refinements")
	if err != nil {
		return nil, xerrors.Errorf("failed to probe session_refinements table: %w", err)
	}
	if !exists {
		return nil, nil
	}

	rows, err := db.QueryContext(
		ctx,
		listSessionWakeSummariesQuery,
		workspace.String(),
		excludeSessionID.String(),
		limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to list session wake summaries: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	out := make([]queryservice.SessionWakeSummary, 0)
	for rows.Next() {
		var (
			sessionID string
			summary   string
		)
		if err := rows.Scan(&sessionID, &summary); err != nil {
			return nil, xerrors.Errorf("failed to scan session wake summary: %w", err)
		}
		out = append(out, queryservice.SessionWakeSummary{
			SessionID: types.SessionID(sessionID),
			Summary:   summary,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate session wake summaries: %w", err)
	}
	return out, nil
}
