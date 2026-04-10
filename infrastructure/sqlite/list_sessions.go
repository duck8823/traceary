package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
)

//go:embed sql/list_sessions.sql
var listSessionsQuery string

var _ port.SessionSummaryFinder = (*Datasource)(nil)

// ListSessionSummaries returns aggregated session information.
func (d *Datasource) ListSessionSummaries(
	ctx context.Context,
	dbPath string,
	input port.ListSessionsInput,
) ([]*port.SessionSummary, error) {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for session list: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	fromValue := ""
	if input.From != nil {
		fromValue = formatTimestamp(*input.From)
	}
	toValue := ""
	if input.To != nil {
		toValue = formatTimestamp(input.To.AddDate(0, 0, 1))
	}

	rows, err := db.QueryContext(
		ctx,
		listSessionsQuery,
		input.SessionID, input.SessionID,
		input.Repo, input.Repo,
		input.Client, input.Client,
		input.Agent, input.Agent,
		input.Label, input.Label,
		fromValue, fromValue,
		toValue, toValue,
		input.Limit, input.Offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query session summaries: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	summaries := make([]*port.SessionSummary, 0)
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
}) (*port.SessionSummary, error) {
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
		return nil, xerrors.Errorf("failed to scan session summary: %w", err)
	}

	startedAt, err := time.Parse(time.RFC3339Nano, startedAtStr)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse started_at: %w", err)
	}

	var endedAt *time.Time
	status := "active"
	if endedAtStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, endedAtStr.String)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse ended_at: %w", err)
		}
		endedAt = &t
		status = "ended"
	} else if time.Since(startedAt) > 24*time.Hour {
		status = "stale"
	}

	var agents []string
	if agentsStr.Valid && agentsStr.String != "" {
		agents = strings.Split(agentsStr.String, ",")
	}

	return &port.SessionSummary{
		SessionID:       sessionID,
		Repo:            repo,
		StartedAt:       startedAt,
		EndedAt:         endedAt,
		Status:          status,
		TotalEvents:     totalEvents,
		CommandCount:    commandCount,
		Agents:          agents,
		Label:           label,
		Summary:         summary,
		ParentSessionID: parentSessionID,
	}, nil
}
