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

//go:embed sql/select_session_ended_at.sql
var selectSessionEndedAtQuery string

//go:embed sql/insert_session.sql
var insertSessionQuery string

//go:embed sql/update_session.sql
var updateSessionQuery string

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
// On session start, a new row is inserted.
// On session end or label/summary update, the existing row is updated.
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

	if session.EndedAt().IsPresent() {
		// Check if session is already ended
		var existingEndedAt sql.NullString
		_ = db.QueryRowContext(
			ctx,
			selectSessionEndedAtQuery,
			session.SessionID().String(),
		).Scan(&existingEndedAt)
		if existingEndedAt.Valid {
			slog.Warn("session already ended, overwriting ended_at",
				"session_id", session.SessionID().String(),
				"existing_ended_at", existingEndedAt.String,
			)
		}
	}

	// Try insert first (INSERT OR IGNORE — no-op if session already exists)
	var parentSessionID *string
	if session.ParentSessionID().String() != "" {
		v := session.ParentSessionID().String()
		parentSessionID = &v
	}
	result, err := db.ExecContext(
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
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to check rows affected: %w", err)
	}

	// If insert succeeded (new session start) and no mutable updates needed, we're done
	if rowsAffected > 0 && !session.EndedAt().IsPresent() && session.Label() == "" && session.Summary() == "" {
		return nil
	}

	// Session already exists or has mutable fields to update — update all mutable fields
	if rowsAffected == 0 || session.EndedAt().IsPresent() || session.Label() != "" || session.Summary() != "" {
		var endedAtValue *string
		if session.EndedAt().IsPresent() {
			endedAt, _ := session.EndedAt().Get()
			v := formatTimestamp(endedAt)
			endedAtValue = &v
		}
		if _, err := db.ExecContext(
			ctx,
			updateSessionQuery,
			endedAtValue,
			session.Label(),
			session.Summary(),
			session.SessionID().String(),
		); err != nil {
			return xerrors.Errorf("failed to update session: %w", err)
		}
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
