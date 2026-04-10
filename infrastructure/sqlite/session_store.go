package sqlite

import (
	"context"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

var _ usecase.SessionSaver = (*Datasource)(nil)

// SaveSession creates or updates a session record.
// On session start, a new row is inserted.
// On session end, the existing row is updated with ended_at.
func (d *Datasource) SaveSession(ctx context.Context, dbPath string, session *model.Session) error {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("failed to open DB for session save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	if session.EndedAt() != nil {
		// Session end: update ended_at
		result, err := db.ExecContext(
			ctx,
			`UPDATE sessions SET ended_at = ?, summary = CASE WHEN ? != '' THEN ? ELSE summary END WHERE session_id = ?`,
			formatTimestamp(*session.EndedAt()),
			session.Summary(),
			session.Summary(),
			session.SessionID().String(),
		)
		if err != nil {
			return xerrors.Errorf("failed to update session ended_at: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return xerrors.Errorf("failed to check rows affected: %w", err)
		}
		if rowsAffected == 0 {
			slog.Debug("session not found for ended_at update, skipping", "session_id", session.SessionID().String())
		}
		return nil
	}

	// Session start: insert new row
	_, err = db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO sessions (session_id, started_at, client, agent, repo) VALUES (?, ?, ?, ?, ?)`,
		session.SessionID().String(),
		formatTimestamp(session.StartedAt()),
		session.Client(),
		session.Agent().String(),
		session.Repo(),
	)
	if err != nil {
		return xerrors.Errorf("failed to insert session: %w", err)
	}

	return nil
}
