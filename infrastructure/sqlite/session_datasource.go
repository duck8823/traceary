package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/insert_session.sql
var insertSessionQuery string

//go:embed sql/update_session_label.sql
var updateSessionLabelQuery string

//go:embed sql/update_session_end.sql
var updateSessionEndQuery string

//go:embed sql/select_session_by_id.sql
var selectSessionByIDQuery string

//go:embed sql/find_latest_session.sql
var findLatestSessionQuery string

//go:embed sql/list_sessions.sql
var listSessionsQuery string

// SessionDatasource is the SQLite-backed implementation of the session
// repository and session query service.
type SessionDatasource struct {
	db *Database
}

// NewSessionDatasource creates a new SessionDatasource bound to the given
// database.
func NewSessionDatasource(db *Database) *SessionDatasource {
	return &SessionDatasource{db: db}
}

// Compile-time interface assertions.
var (
	_ model.SessionRepository           = (*SessionDatasource)(nil)
	_ queryservice.SessionQueryService  = (*SessionDatasource)(nil)
)

// Save creates or updates a session record.
//
// The update path is column-scoped to avoid lost updates between concurrent
// writers. When the aggregate carries an ended_at value, only ended_at and
// summary are written (guarded by ended_at IS NULL to reject duplicate ends).
// When ended_at is empty, only label is written. This keeps session end and
// session label operations in orthogonal columns even when they race.
func (d *SessionDatasource) Save(ctx context.Context, session *model.Session) error {
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for session save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	return saveSession(ctx, db, session)
}

// SaveBoundary atomically persists a session aggregate together with its
// boundary event. Both writes are committed in a single transaction.
func (d *SessionDatasource) SaveBoundary(ctx context.Context, session *model.Session, event *model.Event) error {
	if session == nil {
		return xerrors.Errorf("session must not be nil")
	}
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for session boundary save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin session boundary transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	if _, err := tx.ExecContext(
		ctx,
		insertEventQuery,
		event.EventID().String(),
		event.Kind().String(),
		event.Client().String(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Workspace().String(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("failed to insert boundary event: %w", err)
	}

	if err := saveSession(ctx, tx, session); err != nil {
		return xerrors.Errorf("failed to save session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit session boundary transaction: %w", err)
	}

	return nil
}

// sqlExecer abstracts *sql.DB and *sql.Tx so saveSession can run in either a
// standalone operation or an existing transaction.
type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// saveSession persists a session aggregate. The insert is idempotent
// (INSERT OR IGNORE) so start is a no-op on existing rows. The subsequent
// update is column-scoped so session end and session label cannot clobber
// each other's columns when they race.
func saveSession(ctx context.Context, exec sqlExecer, session *model.Session) error {
	var parentSessionID *string
	if session.ParentSessionID().String() != "" {
		v := session.ParentSessionID().String()
		parentSessionID = &v
	}
	result, err := exec.ExecContext(
		ctx,
		insertSessionQuery,
		session.SessionID().String(),
		formatTimestamp(session.StartedAt()),
		session.Client().String(),
		session.Agent().String(),
		session.Workspace().String(),
		parentSessionID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") && session.ParentSessionID().String() != "" {
			return xerrors.Errorf("parent session not found: %s", session.ParentSessionID())
		}
		return xerrors.Errorf("failed to insert session: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to check rows affected: %w", err)
	}

	// Fresh insert with no mutable state — start is complete.
	if inserted > 0 && !session.EndedAt().IsPresent() && session.Label() == "" {
		return nil
	}

	if session.EndedAt().IsPresent() {
		endedAt, _ := session.EndedAt().Get()
		res, err := exec.ExecContext(
			ctx,
			updateSessionEndQuery,
			formatTimestamp(endedAt),
			session.Summary(),
			session.SessionID().String(),
		)
		if err != nil {
			return xerrors.Errorf("failed to end session: %w", err)
		}
		updated, err := res.RowsAffected()
		if err != nil {
			return xerrors.Errorf("failed to check rows affected: %w", err)
		}
		if updated == 0 {
			return xerrors.Errorf("cannot end session %s: %w", session.SessionID(), model.ErrInvalidSessionState)
		}
		return nil
	}

	if _, err := exec.ExecContext(
		ctx,
		updateSessionLabelQuery,
		session.Label(),
		session.SessionID().String(),
	); err != nil {
		return xerrors.Errorf("failed to update session label: %w", err)
	}
	return nil
}

// FindByID returns the session for the given ID.
// Returns an empty Optional when the session does not exist.
func (d *SessionDatasource) FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*model.Session], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.Empty[*model.Session](), xerrors.Errorf("failed to open DB for session lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	row := db.QueryRowContext(ctx, selectSessionByIDQuery, sessionID.String())

	var (
		sessionIDValue       string
		startedAtValue       string
		endedAtValue         sql.NullString
		clientValue          string
		agentValue           string
		workspaceValue       string
		labelValue           string
		summaryValue         string
		parentSessionIDValue string
	)

	if err := row.Scan(
		&sessionIDValue,
		&startedAtValue,
		&endedAtValue,
		&clientValue,
		&agentValue,
		&workspaceValue,
		&labelValue,
		&summaryValue,
		&parentSessionIDValue,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.Empty[*model.Session](), nil
		}
		return types.Empty[*model.Session](), xerrors.Errorf("failed to scan session row: %w", err)
	}

	sid, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		return types.Empty[*model.Session](), xerrors.Errorf("failed to restore session ID: %w", err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, startedAtValue)
	if err != nil {
		return types.Empty[*model.Session](), xerrors.Errorf("failed to parse started_at: %w", err)
	}
	agent, err := types.AgentOf(agentValue)
	if err != nil {
		return types.Empty[*model.Session](), xerrors.Errorf("failed to restore agent: %w", err)
	}

	endedAt := types.Empty[time.Time]()
	if endedAtValue.Valid {
		t, err := time.Parse(time.RFC3339Nano, endedAtValue.String)
		if err != nil {
			return types.Empty[*model.Session](), xerrors.Errorf("failed to parse ended_at: %w", err)
		}
		endedAt = types.Of(t)
	}

	return types.Of(model.SessionOf(
		sid,
		startedAt,
		endedAt,
		types.Client(clientValue),
		agent,
		types.Workspace(workspaceValue),
		labelValue,
		summaryValue,
		types.SessionID(parentSessionIDValue),
	)), nil
}

// FindLatest returns the session_started event for the latest matching
// session. Returns an empty Optional when no matching session exists.
func (d *SessionDatasource) FindLatest(
	ctx context.Context,
	client types.Client, agent types.Agent, workspace types.Workspace,
	activeOnly bool,
) (types.Optional[*model.Event], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.Empty[*model.Event](), xerrors.Errorf("failed to open DB for latest session lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	row := db.QueryRowContext(
		ctx,
		findLatestSessionQuery,
		types.EventKindSessionStarted.String(),
		types.EventKindSessionEnded.String(),
		types.EventKindSessionStarted.String(),
		types.EventKindSessionEnded.String(),
		types.EventKindSessionStarted.String(),
		client.String(), client.String(),
		agent.String(), agent.String(),
		workspace.String(), workspace.String(),
		types.EventKindSessionStarted.String(),
		activeOnly,
		types.EventKindSessionEnded.String(),
		activeOnly,
		activeOnly,
	)

	event, err := scanEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.Empty[*model.Event](), nil
		}
		return types.Empty[*model.Event](), xerrors.Errorf("failed to restore latest session event: %w", err)
	}

	return types.Of(event), nil
}

// ListSummaries returns aggregated session information.
func (d *SessionDatasource) ListSummaries(
	ctx context.Context,
	limit, offset int,
	sessionID types.SessionID, workspace types.Workspace, client types.Client, agent types.Agent, label string,
	from, to types.Optional[time.Time],
) ([]apptypes.SessionSummary, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for session list: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	fromValue := ""
	if fromTime, ok := from.Get(); ok {
		fromValue = formatTimestamp(fromTime)
	}
	toValue := ""
	if toTime, ok := to.Get(); ok {
		toValue = formatTimestamp(toTime.AddDate(0, 0, 1))
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

	endedAt := types.Empty[time.Time]()
	status := "active"
	if endedAtStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, endedAtStr.String)
		if err != nil {
			return apptypes.SessionSummary{}, xerrors.Errorf("failed to parse ended_at: %w", err)
		}
		endedAt = types.Of(t)
		status = "ended"
	} else if time.Since(startedAt) > 24*time.Hour {
		status = "stale"
	}

	var agents []string
	if agentsStr.Valid && agentsStr.String != "" {
		agents = strings.Split(agentsStr.String, ",")
	}

	return apptypes.SessionSummaryOf(
		types.SessionID(sessionID),
		types.Workspace(repo),
		startedAt,
		endedAt,
		status,
		totalEvents,
		commandCount,
		agents,
		label,
		summary,
		types.SessionID(parentSessionID),
	), nil
}
