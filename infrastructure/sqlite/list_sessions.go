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
	if input.From != "" {
		if _, err := time.Parse("2006-01-02", input.From); err != nil {
			return nil, xerrors.Errorf("invalid from date %q: %w", input.From, err)
		}
		fromValue = input.From + "T00:00:00Z"
	}
	toValue := ""
	if input.To != "" {
		parsed, err := time.Parse("2006-01-02", input.To)
		if err != nil {
			return nil, xerrors.Errorf("invalid to date %q: %w", input.To, err)
		}
		toValue = formatTimestamp(parsed.AddDate(0, 0, 1))
	}

	rows, err := db.QueryContext(
		ctx,
		`SELECT
		   e.session_id,
		   MAX(e.repo) AS repo,
		   MIN(e.created_at) AS started_at,
		   MAX(CASE WHEN e.kind = 'session_ended' THEN e.created_at END) AS ended_at,
		   COUNT(*) AS total_events,
		   SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
		   GROUP_CONCAT(DISTINCT e.agent) AS agents
		 FROM events e
		 WHERE (? = '' OR e.repo = ?)
		   AND (? = '' OR e.agent = ?)
		   AND (? = '' OR e.created_at >= ?)
		   AND (? = '' OR e.created_at < ?)
		 GROUP BY e.session_id
		 ORDER BY started_at DESC
		 LIMIT ? OFFSET ?`,
		input.Repo, input.Repo,
		input.Agent, input.Agent,
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
		sessionID    string
		repo         string
		startedAtStr string
		endedAtStr   sql.NullString
		totalEvents  int
		commandCount int
		agentsStr    sql.NullString
	)

	if err := row.Scan(
		&sessionID,
		&repo,
		&startedAtStr,
		&endedAtStr,
		&totalEvents,
		&commandCount,
		&agentsStr,
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
	}

	var agents []string
	if agentsStr.Valid && agentsStr.String != "" {
		agents = strings.Split(agentsStr.String, ",")
	}

	return &queryservice.SessionSummary{
		SessionID:    sessionID,
		Repo:         repo,
		StartedAt:    startedAt,
		EndedAt:      endedAt,
		Status:       status,
		TotalEvents:  totalEvents,
		CommandCount: commandCount,
		Agents:       agents,
	}, nil
}
