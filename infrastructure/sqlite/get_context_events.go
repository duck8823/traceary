package sqlite

import (
	"context"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

var _ port.ContextEventFinder = (*Datasource)(nil)

// GetContextEvents returns events matching the requested context in descending time order.
func (d *Datasource) GetContextEvents(
	ctx context.Context,
	dbPath string,
	input port.GetContextInput,
) ([]*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for context lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	trimmedRepo := strings.TrimSpace(input.Repo)
	trimmedSessionID := strings.TrimSpace(input.SessionID)
	rows, err := db.QueryContext(
		ctx,
		`SELECT id, kind, client, agent, session_id, repo, body, created_at
		   FROM events
		  WHERE (? = '' OR repo = ?)
		    AND (? = '' OR session_id = ?)
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`,
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
