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

//go:embed sql/find_latest_session.sql
var findLatestSessionQuery string

var _ port.LatestSessionFinder = (*Datasource)(nil)

// FindLatestSessionStartedEvent returns the latest session_started event.
func (d *Datasource) FindLatestSessionStartedEvent(
	ctx context.Context,
	dbPath string,
	input port.FindLatestSessionInput,
) (*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for latest session lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	row := db.QueryRowContext(
		ctx,
		findLatestSessionQuery,
		types.EventKindSessionStarted.String(),
		types.EventKindSessionEnded.String(),
		types.EventKindSessionStarted.String(),
		types.EventKindSessionEnded.String(),
		types.EventKindSessionStarted.String(),
		input.Client, input.Client,
		input.Agent, input.Agent,
		input.Repo, input.Repo,
		types.EventKindSessionStarted.String(),
		input.ActiveOnly,
		types.EventKindSessionEnded.String(),
		input.ActiveOnly,
		input.ActiveOnly,
	)

	event, err := d.scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if input.ActiveOnly {
				return nil, port.ErrActiveSessionNotFound
			}
			return nil, port.ErrSessionNotFound
		}
		return nil, xerrors.Errorf("failed to restore latest session event: %w", err)
	}

	return event, nil
}
