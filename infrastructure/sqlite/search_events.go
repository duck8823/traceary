package sqlite

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
)

var _ queryservice.EventSearcher = (*Datasource)(nil)

// SearchEvents は条件に一致するイベントを新しい順に返します。
func (d *Datasource) SearchEvents(
	ctx context.Context,
	dbPath string,
	input queryservice.SearchEventsInput,
) ([]*model.Event, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("検索用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

	queryValue := strings.TrimSpace(input.Query)
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	fromValue := ""
	if !input.From.IsZero() {
		fromValue = formatTimestamp(input.From)
	}
	toValue := ""
	if !input.To.IsZero() {
		toValue = formatTimestamp(input.To)
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT DISTINCT e.id, e.kind, e.client, e.agent, e.session_id, e.repo, e.body, e.created_at
		   FROM events e
		   LEFT JOIN command_audits a ON a.event_id = e.id
		  WHERE (? = '' OR
		         e.body LIKE ? ESCAPE '\' OR
		         COALESCE(a.command_text, '') LIKE ? ESCAPE '\' OR
		         COALESCE(a.input_text, '') LIKE ? ESCAPE '\' OR
		         COALESCE(a.output_text, '') LIKE ? ESCAPE '\')
		    AND (? = '' OR e.repo = ?)
		    AND (? = '' OR e.session_id = ?)
		    AND (? = '' OR e.client = ?)
		    AND (? = '' OR e.agent = ?)
		    AND (? = '' OR e.kind = ?)
		    AND (? = '' OR e.created_at >= ?)
		    AND (? = '' OR e.created_at < ?)
		  ORDER BY e.created_at DESC, e.id DESC
		  LIMIT ? OFFSET ?`,
		queryValue,
		likeQuery,
		likeQuery,
		likeQuery,
		likeQuery,
		input.Repo,
		input.Repo,
		input.SessionID,
		input.SessionID,
		input.Client,
		input.Client,
		input.Agent,
		input.Agent,
		input.Kind,
		input.Kind,
		fromValue,
		fromValue,
		toValue,
		toValue,
		input.Limit,
		input.Offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("イベント検索クエリに失敗しました: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]*model.Event, 0, input.Limit)
	for rows.Next() {
		event, err := d.scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("検索結果行の復元に失敗しました: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("検索結果の走査に失敗しました: %w", err)
	}

	return events, nil
}

func escapeLikeQuery(query string) string {
	replacer := strings.NewReplacer(
		`\\`, `\\\\`,
		`%`, `\%`,
		`_`, `\_`,
	)

	return replacer.Replace(query)
}
