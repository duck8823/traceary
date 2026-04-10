package sqlite

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
)

var _ queryservice.SessionSummaryFinder = (*Datasource)(nil)

// ListSessionSummaries returns aggregated session information.
func (d *Datasource) ListSessionSummaries(
	ctx context.Context,
	dbPath string,
	input queryservice.ListSessionsInput,
) ([]*queryservice.SessionSummary, error) {
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
		`SELECT
		   s.session_id,
		   s.repo,
		   s.started_at,
		   s.ended_at,
		   COALESCE(agg.total_events, 0) AS total_events,
		   COALESCE(agg.command_count, 0) AS command_count,
		   COALESCE(agg.agents, '') AS agents,
		   s.label,
		   s.summary,
		   COALESCE(s.parent_session_id, '') AS parent_session_id
		 FROM sessions s
		 LEFT JOIN (
		   SELECT
		     e.session_id,
		     COUNT(*) AS total_events,
		     SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
		     GROUP_CONCAT(DISTINCT e.agent) AS agents
		   FROM events e
		   GROUP BY e.session_id
		 ) agg ON agg.session_id = s.session_id
		 WHERE (? = '' OR s.repo = ?)
		   AND (? = '' OR s.agent = ?)
		   AND (? = '' OR s.label = ?)
		   AND (? = '' OR s.started_at >= ?)
		   AND (? = '' OR s.started_at < ?)
		 ORDER BY s.started_at DESC
		 LIMIT ? OFFSET ?`,
		input.Repo, input.Repo,
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

	summaries := make([]*queryservice.SessionSummary, 0)
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
}) (*queryservice.SessionSummary, error) {
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

	return &queryservice.SessionSummary{
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
