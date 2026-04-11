package sqlite

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

//go:embed sql/search_events.sql
var searchEventsQuery string

var _ port.EventSearcher = (*Datasource)(nil)

// SearchEvents returns matching events in descending time order.
func (d *Datasource) SearchEvents(
	ctx context.Context,
	input port.SearchEventsInput,
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
		searchEventsQuery,
		queryValue,
		likeQuery,
		likeQuery,
		likeQuery,
		likeQuery,
		input.Workspace,
		input.Workspace,
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
		boolToInt(input.FailuresOnly),
		input.Limit,
		input.Offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query events: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	events := make([]*model.Event, 0, input.Limit)
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
