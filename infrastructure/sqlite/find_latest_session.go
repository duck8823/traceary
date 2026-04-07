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
				return nil, xerrors.Errorf("条件に一致する active session は存在しません")
			}
			return nil, xerrors.Errorf("条件に一致する session は存在しません")
		}
		return nil, xerrors.Errorf("直近セッションイベントの復元に失敗しました: %w", err)
	}

	return event, nil
}
