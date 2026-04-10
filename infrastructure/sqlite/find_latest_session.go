package sqlite

import (
	"context"
	"log/slog"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

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
		`WITH candidate_sessions AS (
		     SELECT started.id,
		            started.kind,
		            started.client,
		            started.agent,
		            started.session_id,
		            started.repo,
		            started.body,
		            started.created_at,
		            (
		              SELECT boundary.created_at
		                FROM events boundary
		               WHERE boundary.session_id = started.session_id
		                 AND boundary.client = started.client
		                 AND boundary.agent = started.agent
		                 AND boundary.repo = started.repo
		                 AND boundary.kind IN (?, ?)
		               ORDER BY boundary.created_at DESC, boundary.id DESC
		               LIMIT 1
		            ) AS latest_boundary_created_at,
		            (
		              SELECT boundary.id
		                FROM events boundary
		               WHERE boundary.session_id = started.session_id
		                 AND boundary.client = started.client
		                 AND boundary.agent = started.agent
		                 AND boundary.repo = started.repo
		                 AND boundary.kind IN (?, ?)
		               ORDER BY boundary.created_at DESC, boundary.id DESC
		               LIMIT 1
		            ) AS latest_boundary_id
		       FROM events started
		      WHERE started.kind = ?
		        AND (? = '' OR started.client = ?)
		        AND (? = '' OR started.agent = ?)
		        AND (? = '' OR started.repo = ?)
		        AND NOT EXISTS (
		             SELECT 1
		               FROM events newer_started
		              WHERE newer_started.kind = ?
		                AND newer_started.session_id = started.session_id
		                AND newer_started.client = started.client
		                AND newer_started.agent = started.agent
		                AND newer_started.repo = started.repo
		                AND (
		                     newer_started.created_at > started.created_at OR
		                     (newer_started.created_at = started.created_at AND newer_started.id > started.id)
		                )
		        )
		        AND (
		             ? = 0 OR NOT EXISTS (
		                 SELECT 1
		                   FROM events ended
		                  WHERE ended.kind = ?
		                    AND ended.session_id = started.session_id
		                    AND ended.client = started.client
		                    AND ended.agent = started.agent
		                    AND ended.repo = started.repo
		                    AND (
		                         ended.created_at > started.created_at OR
		                         (ended.created_at = started.created_at AND ended.id > started.id)
		                    )
		             )
		        )
		)
		SELECT id,
		       kind,
		       client,
		       agent,
		       session_id,
		       repo,
		       body,
		       created_at
		  FROM candidate_sessions
		 ORDER BY CASE WHEN ? THEN created_at ELSE latest_boundary_created_at END DESC,
		          CASE WHEN ? THEN id ELSE latest_boundary_id END DESC
		 LIMIT 1`,
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
