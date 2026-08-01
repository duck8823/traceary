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

// hydrateEventPayload is the central logical-body boundary. Metadata-free rows
// are legacy identity. Physical bytes never escape this adapter.
func hydrateEventPayload(ctx context.Context, q queryRowContexter, event *model.Event) (*model.Event, error) {
	if event == nil || !event.BodyAvailability().IsAvailable() {
		return event, nil
	}
	var has int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('events') WHERE name='body_codec')`).Scan(&has); err != nil {
		return nil, xerrors.Errorf("inspect event payload metadata: %w", err)
	}
	if has == 0 {
		return event, nil
	}
	var row payloadRow
	destinations := row.scanDestinations()
	if err := q.QueryRowContext(ctx, `SELECT body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256 FROM events WHERE id=?`, event.EventID().String()).Scan(destinations...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("event payload disappeared: %s", event.EventID())
		}
		return nil, xerrors.Errorf("read event payload row: %w", err)
	}
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		return nil, xerrors.Errorf("decode event %s body: %w", event.EventID(), annotatePayloadError(err, event.EventID().String(), "body"))
	}
	return model.EventOfWithBodyAvailabilityAndSourceHook(event.EventID(), event.Kind(), event.Client(), event.Agent(), event.SessionID(), event.Workspace(), string(plain), event.BodyAvailability(), event.CreatedAt(), event.SourceHook()), nil
}

func hydrateAuditPayload(ctx context.Context, q queryRowContexter, eventID string, column string) (sql.NullString, error) {
	allowed := map[string]string{
		"command": "command_text, command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256",
		"input":   "input_text, input_codec, input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256",
		"output":  "output_text, output_codec, output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256",
	}
	projection, ok := allowed[column]
	if !ok {
		return sql.NullString{}, xerrors.Errorf("unsupported audit payload column")
	}
	var has int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('command_audits') WHERE name='command_codec')`).Scan(&has); err != nil {
		return sql.NullString{}, xerrors.Errorf("inspect audit payload metadata: %w", err)
	}
	if has == 0 {
		var value sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT CASE ? WHEN 'command' THEN command_text WHEN 'input' THEN input_text ELSE output_text END FROM command_audits WHERE event_id=?`, column, eventID).Scan(&value); errors.Is(err, sql.ErrNoRows) {
			return sql.NullString{}, nil
		} else if err != nil {
			return sql.NullString{}, xerrors.Errorf("read legacy audit payload: %w", err)
		}
		return value, nil
	}
	var row payloadRow
	if err := q.QueryRowContext(ctx, `SELECT `+projection+` FROM command_audits WHERE event_id=?`, eventID).Scan(row.scanDestinations()...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.NullString{}, nil
		}
		return sql.NullString{}, xerrors.Errorf("read audit payload row: %w", err)
	}
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		return sql.NullString{}, xerrors.Errorf("decode audit %s %s: %w", eventID, column, annotatePayloadError(err, eventID, column))
	}
	return sql.NullString{String: string(plain), Valid: true}, nil
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
	})
	if err != nil {
		return nil, xerrors.Errorf("restore hydrated command audit: %w", err)
	}
	return restored, nil
}

func loadEventPlaintext(ctx context.Context, q queryRowContexter, eventID string) ([]byte, error) {
	var has int
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('events') WHERE name='body_codec')`).Scan(&has); err != nil {
		return nil, xerrors.Errorf("inspect event payload metadata: %w", err)
	}
	if has == 0 {
		var body []byte
		if err := q.QueryRowContext(ctx, `SELECT body FROM events WHERE id=?`, eventID).Scan(&body); err != nil {
			return nil, xerrors.Errorf("read legacy event payload: %w", err)
		}
		return body, nil
	}
	var row payloadRow
	if err := q.QueryRowContext(ctx, `SELECT body, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256 FROM events WHERE id=?`, eventID).Scan(row.scanDestinations()...); err != nil {
		return nil, xerrors.Errorf("read event payload row: %w", err)
	}
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		return nil, annotatePayloadError(err, eventID, "body")
	}
	return plain, nil
}

func databaseColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, table, column).Scan(&exists); err != nil {
		return false, xerrors.Errorf("inspect payload metadata column: %w", err)
	}
	return exists != 0, nil
}
