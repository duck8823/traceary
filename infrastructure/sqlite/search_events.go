package sqlite

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/search_events.sql
var searchEventsQuery string

// Search returns matching events in descending time order.
func (d *Datasource) Search(
	ctx context.Context,
	query string, workspace types.Workspace, sessionID types.SessionID, client types.Client, agent types.Agent, kind types.EventKind,
	from, to time.Time,
	limit, offset int,
	failuresOnly bool,
) ([]*model.Event, error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event search: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	queryValue := strings.TrimSpace(query)
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	fromValue := ""
	if !from.IsZero() {
		fromValue = formatTimestamp(from)
	}
	toValue := ""
	if !to.IsZero() {
		toValue = formatTimestamp(to)
	}

	rows, err := db.QueryContext(
		ctx,
		searchEventsQuery,
		queryValue,
		likeQuery,
		likeQuery,
		likeQuery,
		likeQuery,
		workspace.String(),
		workspace.String(),
		sessionID.String(),
		sessionID.String(),
		client.String(),
		client.String(),
		agent.String(),
		agent.String(),
		kind.String(),
		kind.String(),
		fromValue,
		fromValue,
		toValue,
		toValue,
		boolToInt(failuresOnly),
		limit,
		offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	events := make([]*model.Event, 0, limit)
	for rows.Next() {
		event, err := d.scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore search result row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate search result rows: %w", err)
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
