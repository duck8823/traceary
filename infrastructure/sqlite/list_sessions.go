package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

//go:embed sql/list_sessions.sql
var listSessionsQuery string

// ListSummaries returns aggregated session information.
func (d *Datasource) ListSummaries(
	ctx context.Context,
	limit, offset int,
	sessionID domtypes.SessionID, workspace domtypes.Workspace, client domtypes.Client, agent domtypes.Agent, label string,
	from, to domtypes.Optional[time.Time],
) ([]apptypes.SessionSummary, error) {
	db, err := d.openDB(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for session list: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	fromValue := ""
	if from.IsPresent() {
		fromValue = formatTimestamp(from.Get())
	}
	toValue := ""
	if to.IsPresent() {
		toValue = formatTimestamp(to.Get().AddDate(0, 0, 1))
	}

	rows, err := db.QueryContext(
		ctx,
		listSessionsQuery,
		sessionID.String(), sessionID.String(),
		workspace.String(), workspace.String(),
		client.String(), client.String(),
		agent.String(), agent.String(),
		label, label,
		fromValue, fromValue,
		toValue, toValue,
		limit, offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query session summaries: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	summaries := make([]apptypes.SessionSummary, 0)
	for rows.Next() {
		summary, err := scanSessionSummary(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan session summary row: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate session summary rows: %w", err)
	}

	return summaries, nil
}

func scanSessionSummary(row interface {
	Scan(dest ...any) error
}) (apptypes.SessionSummary, error) {
	var (
		sessionID       string
		repo            string
		startedAtStr    string
		endedAtStr      sql.NullString
		totalEvents     int
		commandCount    int
		agentsStr       sql.NullString
		label           string
		summary         string
		parentSessionID string
	)

	if err := row.Scan(
		&sessionID,
		&repo,
		&startedAtStr,
		&endedAtStr,
		&totalEvents,
		&commandCount,
		&agentsStr,
		&label,
		&summary,
		&parentSessionID,
	); err != nil {
		return apptypes.SessionSummary{}, xerrors.Errorf("failed to scan session summary: %w", err)
	}

	startedAt, err := time.Parse(time.RFC3339Nano, startedAtStr)
	if err != nil {
		return apptypes.SessionSummary{}, xerrors.Errorf("failed to parse started_at: %w", err)
	}

	endedAt := domtypes.Empty[time.Time]()
	status := "active"
	if endedAtStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, endedAtStr.String)
		if err != nil {
			return apptypes.SessionSummary{}, xerrors.Errorf("failed to parse ended_at: %w", err)
		}
		endedAt = domtypes.Of(t)
		status = "ended"
	} else if time.Since(startedAt) > 24*time.Hour {
		status = "stale"
	}

	var agents []string
	if agentsStr.Valid && agentsStr.String != "" {
		agents = strings.Split(agentsStr.String, ",")
	}

	return apptypes.NewSessionSummary(
		domtypes.SessionID(sessionID),
		domtypes.Workspace(repo),
		startedAt,
		endedAt,
		status,
		totalEvents,
		commandCount,
		agents,
		label,
		summary,
		domtypes.SessionID(parentSessionID),
	), nil
}
