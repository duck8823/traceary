package sqlite

import (
	"context"
	"database/sql"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

var _ queryservice.RecentEventFinder = (*Datasource)(nil)

// Save はイベントを保存します。
func (d *Datasource) Save(ctx context.Context, dbPath string, event *model.Event) error {
	if event == nil {
		return xerrors.Errorf("イベントは nil にできません")
	}

	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("イベント保存用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO events(id, kind, client, agent, session_id, repo, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID().String(),
		event.Kind().String(),
		event.Client(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Repo(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("イベントの INSERT に失敗しました: %w", err)
	}

	return nil
}

// ListRecent は新しい順にイベントを返します。
func (d *Datasource) ListRecent(ctx context.Context, dbPath string, limit int) ([]*model.Event, error) {
	if limit <= 0 {
		return nil, xerrors.Errorf("limit は 1 以上である必要があります")
	}

	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("イベント一覧取得用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(
		ctx,
		`SELECT id, kind, client, agent, session_id, repo, body, created_at
		   FROM events
		  ORDER BY created_at DESC, id DESC
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("イベント一覧クエリに失敗しました: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]*model.Event, 0, limit)
	for rows.Next() {
		event, err := d.scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("イベント行の復元に失敗しました: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("イベント一覧の走査に失敗しました: %w", err)
	}

	return events, nil
}

func (d *Datasource) scanEvent(rowScanner interface {
	Scan(dest ...any) error
}) (*model.Event, error) {
	var (
		eventIDValue   string
		eventKindValue string
		clientValue    string
		agentValue     string
		sessionIDValue string
		repoValue      string
		bodyValue      string
		createdAtValue string
	)

	if err := rowScanner.Scan(
		&eventIDValue,
		&eventKindValue,
		&clientValue,
		&agentValue,
		&sessionIDValue,
		&repoValue,
		&bodyValue,
		&createdAtValue,
	); err != nil {
		return nil, xerrors.Errorf("イベント行の scan に失敗しました: %w", err)
	}

	return d.restoreEvent(
		eventIDValue,
		eventKindValue,
		clientValue,
		agentValue,
		sessionIDValue,
		repoValue,
		bodyValue,
		createdAtValue,
	)
}

func (d *Datasource) restoreEvent(
	eventIDValue string,
	eventKindValue string,
	clientValue string,
	agentValue string,
	sessionIDValue string,
	repoValue string,
	bodyValue string,
	createdAtValue string,
) (*model.Event, error) {
	eventID, err := types.EventIDOf(eventIDValue)
	if err != nil {
		return nil, xerrors.Errorf("event ID の復元に失敗しました: %w", err)
	}
	eventKind, err := types.EventKindOf(eventKindValue)
	if err != nil {
		return nil, xerrors.Errorf("event kind の復元に失敗しました: %w", err)
	}
	agent, err := types.AgentOf(agentValue)
	if err != nil {
		return nil, xerrors.Errorf("agent の復元に失敗しました: %w", err)
	}
	sessionID, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		return nil, xerrors.Errorf("session ID の復元に失敗しました: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return nil, xerrors.Errorf("作成時刻の復元に失敗しました: %w", err)
	}

	return model.EventOf(
		eventID,
		eventKind,
		clientValue,
		agent,
		sessionID,
		repoValue,
		bodyValue,
		createdAt,
	), nil
}

func formatTimestamp(timestamp time.Time) string {
	return timestamp.UTC().Format(time.RFC3339Nano)
}

func (d *Datasource) openDB(ctx context.Context, dbPath string) (_ *sql.DB, err error) {
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, xerrors.Errorf("SQLite 接続の初期化に失敗しました: %w", err)
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, xerrors.Errorf("SQLite への接続確認に失敗しました: %w", err)
	}

	return db, nil
}
