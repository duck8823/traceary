package sqlite

import (
	"context"
	_ "embed"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

//go:embed sql/insert_command_audit.sql
var insertCommandAuditQuery string

var _ port.CommandAuditSaver = (*Datasource)(nil)

// SaveCommandAudit persists an event and command-audit data in one transaction.
func (d *Datasource) SaveCommandAudit(
	ctx context.Context,
	event *model.Event,
	commandAudit *model.CommandAudit,
) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}
	if commandAudit == nil {
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
		event.Repo(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("failed to insert event: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		insertCommandAuditQuery,
		commandAudit.EventID().String(),
		commandAudit.Command(),
		commandAudit.Input(),
		commandAudit.Output(),
		commandAudit.InputTruncated(),
		commandAudit.OutputTruncated(),
		commandAudit.ExitCode(),
	); err != nil {
		return xerrors.Errorf("failed to insert command audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit command audit transaction: %w", err)
	}

	return nil
}
