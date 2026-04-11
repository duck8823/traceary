package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/find_latest_session.sql
var findLatestSessionQuery string

// FindLatest returns the session_started event for the latest matching session.
// Returns an empty Optional when no matching session exists.
func (d *Datasource) FindLatest(
	ctx context.Context,
	client types.Client, agent types.Agent, workspace types.Workspace,
	activeOnly bool,
) (types.Optional[*model.Event], error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return types.Empty[*model.Event](), xerrors.Errorf("failed to open DB for latest session lookup: %w", err)
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
		client.String(), client.String(),
		agent.String(), agent.String(),
		workspace.String(), workspace.String(),
		types.EventKindSessionStarted.String(),
		activeOnly,
		types.EventKindSessionEnded.String(),
		activeOnly,
		activeOnly,
	)

	event, err := d.scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.Empty[*model.Event](), nil
		}
		return types.Empty[*model.Event](), xerrors.Errorf("failed to restore latest session event: %w", err)
	}

	return types.Of(event), nil
}
