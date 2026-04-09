package sqlite

import (
	"context"
	"log/slog"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ usecase.SessionStartedEventFinder = (*Datasource)(nil)

// FindSessionStartedEvent returns the latest session_started event for the target session.
func (d *Datasource) FindSessionStartedEvent(
	ctx context.Context,
	dbPath string,
	sessionID types.SessionID,
) (*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for session_started lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	row := db.QueryRowContext(
		ctx,
		`SELECT id, kind, client, agent, session_id, repo, body, created_at
		   FROM events
		  WHERE kind = ?
		    AND session_id = ?
		  ORDER BY created_at DESC, id DESC
		  LIMIT 1`,
		types.EventKindSessionStarted.String(),
		sessionID.String(),
	)

	event, err := d.scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, usecase.ErrSessionStartedEventNotFound
		}
		return nil, xerrors.Errorf("failed to restore session_started event: %w", err)
	}

	return event, nil
}
