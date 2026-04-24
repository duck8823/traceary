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
	_ model.SessionRepository          = (*SessionDatasource)(nil)
	_ queryservice.SessionQueryService = (*SessionDatasource)(nil)
)

// Save persists a session label change. It is orthogonal to SaveBoundary:
// it writes only the label column (via a defensive idempotent insert plus
// UPDATE label = ?) and never touches ended_at or summary. This lets
// callers label a session regardless of whether it is still active or
// already ended, and keeps session end and session label operations from
// clobbering each other's columns when they race.
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

	return saveSessionLabel(ctx, db, session)
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
		nullableString(event.SourceHook()),
	); err != nil {
		return xerrors.Errorf("failed to insert boundary event: %w", err)
	}

	if err := saveSessionBoundary(ctx, tx, session); err != nil {
		return xerrors.Errorf("failed to save session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit session boundary transaction: %w", err)
	}

	return nil
}

// sqlExecer abstracts *sql.DB and *sql.Tx so the helpers below can run in
// either a standalone operation or an existing transaction.
type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertSessionRowIfMissing inserts the immutable session row if it does
// not already exist. It is shared by the label-only and boundary paths
// because both need to make sure the row is present before updating any
// mutable column. The boolean return reports whether the caller actually
// created a new row (true) or hit the INSERT OR IGNORE no-op on an
// existing row (false), so the caller can decide whether the pre-existing
// state is acceptable.
func insertSessionRowIfMissing(ctx context.Context, exec sqlExecer, session *model.Session) (bool, error) {
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
			return false, xerrors.Errorf("parent session not found: %s", session.ParentSessionID())
		}
		return false, xerrors.Errorf("failed to insert session: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, xerrors.Errorf("failed to check rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

// saveSessionLabel persists a label-only change. It is the Save path for
// session label operations. It never touches ended_at or summary, so it
// works on both active and already-ended sessions and cannot clobber a
// concurrent session end.
func saveSessionLabel(ctx context.Context, exec sqlExecer, session *model.Session) error {
	if _, err := insertSessionRowIfMissing(ctx, exec, session); err != nil {
		return err
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

// saveSessionBoundary persists session start or end state. On start, a
// fresh row must be inserted; an existing row means a prior session start
// for the same session_id has already committed, so the caller's
// transaction must roll back to avoid duplicate session_started events.
// On end, a guarded UPDATE writes ended_at + summary only when ended_at
// IS NULL; zero rows affected means the session was already ended and we
// return ErrInvalidSessionState so the caller's transaction rolls back
// (including the boundary event).
func saveSessionBoundary(ctx context.Context, exec sqlExecer, session *model.Session) error {
	inserted, err := insertSessionRowIfMissing(ctx, exec, session)
	if err != nil {
		return err
	}

	if _, ok := session.EndedAt().Value(); !ok {
		if !inserted {
			return xerrors.Errorf("cannot start session %s: %w", session.SessionID(), model.ErrInvalidSessionState)
		}
		return nil
	}

	endedAt, _ := session.EndedAt().Value()
	result, err := exec.ExecContext(
		ctx,
		updateSessionEndQuery,
		formatTimestamp(endedAt),
		session.Summary(),
		session.SessionID().String(),
	)
	if err != nil {
		return xerrors.Errorf("failed to end session: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to check rows affected: %w", err)
	}
	if updated == 0 {
		return xerrors.Errorf("cannot end session %s: %w", session.SessionID(), model.ErrInvalidSessionState)
	}
	return nil
}

// FindByID returns the session for the given ID.
// Returns an empty Optional when the session does not exist.
func (d *SessionDatasource) FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*model.Session], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.Session](), xerrors.Errorf("failed to open DB for session lookup: %w", err)
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
			return types.None[*model.Session](), nil
		}
		return types.None[*model.Session](), xerrors.Errorf("failed to scan session row: %w", err)
	}

	sid, err := types.SessionIDOf(sessionIDValue)
	if err != nil {
		return types.None[*model.Session](), xerrors.Errorf("failed to restore session ID: %w", err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, startedAtValue)
	if err != nil {
		return types.None[*model.Session](), xerrors.Errorf("failed to parse started_at: %w", err)
	}
	agent, err := types.AgentOf(agentValue)
	if err != nil {
		return types.None[*model.Session](), xerrors.Errorf("failed to restore agent: %w", err)
	}

	endedAt := types.None[time.Time]()
	if endedAtValue.Valid {
		t, err := time.Parse(time.RFC3339Nano, endedAtValue.String)
		if err != nil {
			return types.None[*model.Session](), xerrors.Errorf("failed to parse ended_at: %w", err)
		}
		endedAt = types.Some(t)
	}

	return types.Some(model.SessionOf(
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
		return types.None[*model.Event](), xerrors.Errorf("failed to open DB for latest session lookup: %w", err)
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
			return types.None[*model.Event](), nil
		}
		return types.None[*model.Event](), xerrors.Errorf("failed to restore latest session event: %w", err)
	}

	return types.Some(event), nil
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
	if fromTime, ok := from.Value(); ok {
		fromValue = formatTimestamp(fromTime)
	}
	toValue := ""
	if toTime, ok := to.Value(); ok {
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

	endedAt := types.None[time.Time]()
	status := "active"
	if endedAtStr.Valid {
		t, err := time.Parse(time.RFC3339Nano, endedAtStr.String)
		if err != nil {
			return apptypes.SessionSummary{}, xerrors.Errorf("failed to parse ended_at: %w", err)
		}
		endedAt = types.Some(t)
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
