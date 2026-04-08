package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ queryservice.LatestSessionFinder = (*Datasource)(nil)

// FindLatestSessionStartedEvent returns the latest session_started event.
func (d *Datasource) FindLatestSessionStartedEvent(
	ctx context.Context,
	dbPath string,
	input queryservice.FindLatestSessionInput,
) (*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for latest session lookup: %w", err)
	}
	defer func() { _ = db.Close() }()

	row := db.QueryRowContext(
		ctx,
		`SELECT started.id,
		        started.kind,
		        started.client,
		        started.agent,
		        started.session_id,
		        started.repo,
		        started.body,
		        started.created_at
		   FROM events started
		  WHERE started.kind = ?
		    AND (? = '' OR started.client = ?)
		    AND (? = '' OR started.agent = ?)
		    AND (? = '' OR started.repo = ?)
		    AND (
		         ? = 0 OR NOT EXISTS (
		             SELECT 1
		               FROM events ended
		              WHERE ended.kind = ?
		                AND ended.session_id = started.session_id
		                AND (
		                     ended.created_at > started.created_at OR
		                     (ended.created_at = started.created_at AND ended.id > started.id)
		                )
		         )
		    )
		  ORDER BY started.created_at DESC, started.id DESC
		  LIMIT 1`,
		types.EventKindSessionStarted.String(),
		input.Client, input.Client,
		input.Agent, input.Agent,
		input.Repo, input.Repo,
		input.ActiveOnly,
		types.EventKindSessionEnded.String(),
	)

	event, err := d.scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if input.ActiveOnly {
				return nil, queryservice.ErrActiveSessionNotFound
			}
			return nil, queryservice.ErrSessionNotFound
		}
		return nil, xerrors.Errorf("failed to restore latest session event: %w", err)
	}

	return event, nil
}
