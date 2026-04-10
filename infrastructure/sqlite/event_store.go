package sqlite

import (
	"context"
	"log/slog"
	"database/sql"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
	"github.com/duck8823/traceary/domain/types"
)

var _ port.RecentEventFinder = (*Datasource)(nil)

// Save persists an event.
func (d *Datasource) Save(ctx context.Context, dbPath string, event *model.Event) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}

	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("failed to open DB for event save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

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
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	return nil
}

// ListRecent returns events in descending time order.
func (d *Datasource) ListRecent(
	ctx context.Context,
	dbPath string,
	input port.ListRecentEventsInput,
) ([]*model.Event, error) {
	if input.Limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if input.Offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

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
		`SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.repo, e.body, e.created_at
		   FROM events e
		   LEFT JOIN command_audits ca ON ca.event_id = e.id
		  WHERE (? = '' OR e.kind = ?)
		    AND (? = '' OR e.client = ?)
		    AND (? = '' OR e.agent = ?)
		    AND (? = '' OR e.session_id = ?)
		    AND (? = '' OR e.repo = ?)
		    AND (? = 0 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
		    AND (? = '' OR e.created_at >= ?)
		    AND (? = '' OR e.created_at < ?)
		  ORDER BY e.created_at DESC, e.id DESC
		  LIMIT ? OFFSET ?`,
		input.Kind, input.Kind,
		input.Client, input.Client,
		input.Agent, input.Agent,
		input.SessionID, input.SessionID,
		input.Repo, input.Repo,
		boolToInt(input.FailuresOnly),
		fromValue, fromValue,
		toValue, toValue,
		input.Limit,
		input.Offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query recent events: %w", err)
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
			return nil, xerrors.Errorf("failed to restore event row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate recent event rows: %w", err)
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
		return nil, xerrors.Errorf("failed to scan event row: %w", err)
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
		return nil, xerrors.Errorf("failed to restore event ID: %w", err)
	}
	eventKind, err := types.EventKindOf(eventKindValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore event kind: %w", err)
	}
	agent, err := types.AgentOf(agentValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore agent: %w", err)
	}
	sessionID, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore session ID: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore created_at: %w", err)
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
		return nil, xerrors.Errorf("failed to initialize SQLite connection: %w", err)
	}
	defer func() {
		if err != nil {
			if err := db.Close(); err != nil {
				slog.Debug("failed to close resource", "error", err)
			}
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, xerrors.Errorf("failed to ping SQLite DB: %w", err)
	}

	return db, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
