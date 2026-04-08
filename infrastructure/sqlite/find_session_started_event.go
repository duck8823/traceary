package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ usecase.SessionStartedEventFinder = (*Datasource)(nil)

// FindSessionStartedEvent は対象 session の直近の session_started イベントを返します。
func (d *Datasource) FindSessionStartedEvent(
	ctx context.Context,
	dbPath string,
	sessionID types.SessionID,
) (*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("session_started 取得用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

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
		return nil, xerrors.Errorf("session_started イベントの復元に失敗しました: %w", err)
	}

	return event, nil
}
