package sqlite

import (
	"context"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

var _ usecase.CommandAuditSaver = (*Datasource)(nil)

// SaveCommandAudit persists an event and command-audit data in one transaction.
func (d *Datasource) SaveCommandAudit(
	ctx context.Context,
	dbPath string,
	event *model.Event,
	commandAudit *model.CommandAudit,
) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}
	if commandAudit == nil {
		return xerrors.Errorf("command audit must not be nil")
	}

	db, err := d.openDB(ctx, dbPath)
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

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		commandAudit.EventID().String(),
		commandAudit.Command(),
		commandAudit.Input(),
		commandAudit.Output(),
		commandAudit.InputTruncated(),
		commandAudit.OutputTruncated(),
	); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit command audit transaction: %w", err)
	}

	return nil
}
