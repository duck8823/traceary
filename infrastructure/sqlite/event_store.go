package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/insert_event.sql
var insertEventQuery string

//go:embed sql/insert_command_audit.sql
var insertCommandAuditQuery string

//go:embed sql/select_recent_events.sql
var selectRecentEventsQuery string

// Save persists an event.
func (d *Datasource) Save(ctx context.Context, event *model.Event) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}

	db, err := d.openDB(ctx)
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
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	return nil
}

// SaveWithAudit persists an event together with its command audit.
func (d *Datasource) SaveWithAudit(
	ctx context.Context,
	event *model.Event,
	audit *model.CommandAudit,
) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}
	if audit == nil {
		return xerrors.Errorf("command audit must not be nil")
	}

	db, err := d.openDB(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for command audit save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin command audit transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	if _, err := tx.ExecContext(
		ctx,
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		insertCommandAuditQuery,
		audit.EventID().String(),
		audit.Command(),
		audit.Input(),
		audit.Output(),
		audit.InputTruncated(),
		audit.OutputTruncated(),
		audit.ExitCode(),
	); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit command audit transaction: %w", err)
	}

	return nil
}

// ListRecent returns events in descending time order.
func (d *Datasource) ListRecent(
	ctx context.Context,
	limit, offset int,
	kind, client, agent, sessionID, workspace string,
	failuresOnly bool,
	from, to time.Time,
) ([]*model.Event, error) {
	if limit <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if offset < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}

	db, err := d.openDB(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for event listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

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
		selectRecentEventsQuery,
		kind, kind,
		client, client,
		agent, agent,
		sessionID, sessionID,
		workspace, workspace,
		boolToInt(failuresOnly),
		fromValue, fromValue,
		toValue, toValue,
		limit,
		offset,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query recent events: %w", err)
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

func (d *Datasource) openDB(ctx context.Context) (_ *sql.DB, err error) {
	db, err := sql.Open("sqlite", sqliteDSN(d.dbPath))
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
