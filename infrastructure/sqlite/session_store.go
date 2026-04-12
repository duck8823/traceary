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

// SaveSession creates or updates a session record.
// On session start, a new row is inserted.
// On session end or label/summary update, the existing row is updated.
func (d *Datasource) SaveSession(ctx context.Context, session *model.Session) error {
	db, err := d.openDB(ctx)
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
	if session.ParentSessionID() != "" {
		v := session.ParentSessionID()
		parentSessionID = &v
	}
	result, err := db.ExecContext(
		ctx,
		insertSessionQuery,
		session.SessionID().String(),
		formatTimestamp(session.StartedAt()),
		session.Client(),
		session.Agent().String(),
		session.Workspace(),
		parentSessionID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") && session.ParentSessionID() != "" {
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
func (d *Datasource) FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*model.Session], error) {
	db, err := d.openDB(ctx)
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
		clientValue,
		agent,
		workspaceValue,
		labelValue,
		summaryValue,
		parentSessionIDValue,
	)), nil
}
