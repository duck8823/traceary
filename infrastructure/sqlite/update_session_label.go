package sqlite

import (
	"context"
	_ "embed"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/update_session_label.sql
var updateSessionLabelQuery string

// UpdateSessionLabel sets the label for a session.
func (d *Datasource) UpdateSessionLabel(ctx context.Context, dbPath string, sessionID types.SessionID, label string) error {
	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("failed to open DB: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	result, err := db.ExecContext(
		ctx,
		updateSessionLabelQuery,
		label,
		sessionID.String(),
	)
	if err != nil {
		return xerrors.Errorf("failed to update session label: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return xerrors.Errorf("session not found: %s", sessionID)
	}

	return nil
}
