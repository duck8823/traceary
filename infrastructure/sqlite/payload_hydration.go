package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
)

type queryRowContexter interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// hydrateEventPayload is the central logical-body boundary. Bodies are stored
// as plaintext; the listed event already holds the stored text.
func hydrateEventPayload(_ context.Context, _ queryRowContexter, event *model.Event) (*model.Event, error) {
	return event, nil
}

func hydrateAuditPayload(ctx context.Context, q queryRowContexter, eventID string, column string) (sql.NullString, error) {
	physicalColumns := map[string]string{"command": "command_text", "input": "input_text", "output": "output_text"}
	physicalColumn, ok := physicalColumns[column]
	if !ok {
		return sql.NullString{}, xerrors.Errorf("unsupported audit payload column")
	}
	var value sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT `+physicalColumn+` FROM command_audits WHERE event_id=?`, eventID).Scan(&value); errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, nil
	} else if err != nil {
		return sql.NullString{}, xerrors.Errorf("read audit payload: %w", err)
	}
	return value, nil
}

func hydrateCommandAudit(ctx context.Context, q queryRowContexter, audit *model.CommandAudit) (*model.CommandAudit, error) {
	if audit == nil {
		return nil, nil
	}
	command, err := hydrateAuditPayload(ctx, q, audit.EventID().String(), "command")
	if err != nil {
		return nil, err
	}
	input, err := hydrateAuditPayload(ctx, q, audit.EventID().String(), "input")
	if err != nil {
		return nil, err
	}
	output, err := hydrateAuditPayload(ctx, q, audit.EventID().String(), "output")
	if err != nil {
		return nil, err
	}
	wrapper := audit.CommandIdentity().Wrapper()
	restored, err := model.CommandAuditFromSnapshot(model.CommandAuditSnapshot{
		EventID: audit.EventID(), Command: command.String, Wrapper: wrapper,
		CommandName: audit.CommandIdentity().Command(), Input: input.String, Output: output.String,
		InputTruncated: audit.InputTruncated(), OutputTruncated: audit.OutputTruncated(),
		InputOriginalBytes: audit.InputOriginalBytes(), OutputOriginalBytes: audit.OutputOriginalBytes(),
		ExitCode: audit.ExitCode(), Failed: audit.Failed(), FailureReason: audit.FailureReason(),
		OutputMetadata: audit.OutputMetadata(),
	})
	if err != nil {
		return nil, xerrors.Errorf("restore hydrated command audit: %w", err)
	}
	return restored, nil
}

func loadEventPlaintext(ctx context.Context, q queryRowContexter, eventID string) ([]byte, error) {
	var body []byte
	if err := q.QueryRowContext(ctx, `SELECT body FROM events WHERE id=?`, eventID).Scan(&body); err != nil {
		return nil, xerrors.Errorf("read event body: %w", err)
	}
	return body, nil
}

func databaseTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`,
		table,
	).Scan(&exists); err != nil {
		return false, xerrors.Errorf("inspect table presence: %w", err)
	}
	return exists != 0, nil
}

func databaseColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, table, column).Scan(&exists); err != nil {
		return false, xerrors.Errorf("inspect payload metadata column: %w", err)
	}
	return exists != 0, nil
}
