package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ queryservice.LatestSessionFinder = (*Datasource)(nil)

// FindLatestSessionStartedEvent は直近の session_started イベントを返します。
func (d *Datasource) FindLatestSessionStartedEvent(
	ctx context.Context,
	dbPath string,
	input queryservice.FindLatestSessionInput,
) (*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("直近セッション取得用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

	row := db.QueryRowContext(
		ctx,
		`SELECT id, kind, client, agent, session_id, repo, body, created_at
		   FROM events
		  WHERE kind = ?
		    AND (? = '' OR client = ?)
		    AND (? = '' OR agent = ?)
		    AND (? = '' OR repo = ?)
		  ORDER BY created_at DESC, id DESC
		  LIMIT 1`,
		types.EventKindSessionStarted.String(),
		input.Client, input.Client,
		input.Agent, input.Agent,
		input.Repo, input.Repo,
	)

	event, err := d.scanEvent(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, xerrors.Errorf("条件に一致する session は存在しません")
		}
		return nil, xerrors.Errorf("直近セッションイベントの復元に失敗しました: %w", err)
	}

	return event, nil
}
