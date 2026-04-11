package sqlite

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

//go:embed sql/get_context_events.sql
var getContextEventsQuery string

var _ port.ContextEventFinder = (*Datasource)(nil)

// GetContextEvents returns events matching the requested context in descending time order.
func (d *Datasource) GetContextEvents(
	ctx context.Context,
	input port.GetContextInput,
) ([]*model.Event, error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for context lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	trimmedRepo := strings.TrimSpace(input.Workspace)
	trimmedSessionID := strings.TrimSpace(input.SessionID)
	rows, err := db.QueryContext(
		ctx,
		getContextEventsQuery,
		trimmedRepo,
		trimmedRepo,
		trimmedSessionID,
		trimmedSessionID,
		input.Limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query context events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	events := make([]*model.Event, 0, input.Limit)
	for rows.Next() {
		event, err := d.scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore context event row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate context event rows: %w", err)
	}

	return events, nil
}
