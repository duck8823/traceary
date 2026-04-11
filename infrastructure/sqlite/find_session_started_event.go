package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/find_session_started_event.sql
var findSessionStartedEventQuery string

var _ port.SessionStartedEventFinder = (*Datasource)(nil)

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
		findSessionStartedEventQuery,
		types.EventKindSessionStarted.String(),
		sessionID.String(),
	)

	event, err := d.scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, port.ErrSessionStartedEventNotFound
		}
		return nil, xerrors.Errorf("failed to restore session_started event: %w", err)
	}

	return event, nil
}
