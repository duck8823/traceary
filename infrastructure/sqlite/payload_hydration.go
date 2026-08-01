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
		if int64(len(event.Body())) > maxDecodedPayloadBytes {
			return nil, &PayloadIntegrityError{Codec: payloadCodecIdentity, RowID: event.EventID().String(), Field: "body", Reason: "decoded length exceeds limit"}
		}
		return event, nil
	}
	var row payloadRow
	var storedLength sql.NullInt64
	destinations := append(row.scanDestinations(), &storedLength)
	if err := q.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(body AS BLOB)) <= ? THEN body END, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256, length(CAST(body AS BLOB)) FROM events WHERE id=?`, maxStoredPayloadBytes, event.EventID().String()).Scan(destinations...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("event payload disappeared: %s", event.EventID())
		}
		return nil, xerrors.Errorf("read event payload row: %w", err)
	}
	if storedLength.Valid && storedLength.Int64 > maxStoredPayloadBytes {
		return nil, &PayloadIntegrityError{Codec: row.Codec.String, RowID: event.EventID().String(), Field: "body", Reason: "stored length exceeds limit"}
	}
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err != nil {
		return nil, xerrors.Errorf("decode event %s body: %w", event.EventID(), annotatePayloadError(err, event.EventID().String(), "body"))
	}
	return model.EventOfWithBodyAvailabilityAndSourceHook(event.EventID(), event.Kind(), event.Client(), event.Agent(), event.SessionID(), event.Workspace(), string(plain), event.BodyAvailability(), event.CreatedAt(), event.SourceHook()), nil
}

func hydrateAuditPayload(ctx context.Context, q queryRowContexter, eventID string, column string) (sql.NullString, error) {
	allowed := map[string]string{
		"command": "command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256",
		"input":   "input_codec, input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256",
		"output":  "output_codec, output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256",
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
		var storedLength sql.NullInt64
		if err := q.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(CASE ? WHEN 'command' THEN command_text WHEN 'input' THEN input_text ELSE output_text END AS BLOB)) <= ? THEN CASE ? WHEN 'command' THEN command_text WHEN 'input' THEN input_text ELSE output_text END END, length(CAST(CASE ? WHEN 'command' THEN command_text WHEN 'input' THEN input_text ELSE output_text END AS BLOB)) FROM command_audits WHERE event_id=?`, column, maxDecodedPayloadBytes, column, column, eventID).Scan(&value, &storedLength); errors.Is(err, sql.ErrNoRows) {
			return sql.NullString{}, nil
		} else if err != nil {
			return sql.NullString{}, xerrors.Errorf("read legacy audit payload: %w", err)
		}
		if storedLength.Valid && storedLength.Int64 > maxDecodedPayloadBytes {
			return sql.NullString{}, &PayloadIntegrityError{Codec: "identity", RowID: eventID, Field: column, Reason: "stored length exceeds limit"}
		}
		return value, nil
	}
	physicalColumns := map[string]string{"command": "command_text", "input": "input_text", "output": "output_text"}
	physicalColumn := physicalColumns[column]
	var row payloadRow
	var storedLength sql.NullInt64
	destinations := append(row.scanDestinations(), &storedLength)
	boundedProjection := `CASE WHEN length(CAST(` + physicalColumn + ` AS BLOB)) <= ? THEN ` + physicalColumn + ` END, ` + projection + `, length(CAST(` + physicalColumn + ` AS BLOB))`
	if err := q.QueryRowContext(ctx, `SELECT `+boundedProjection+` FROM command_audits WHERE event_id=?`, maxStoredPayloadBytes, eventID).Scan(destinations...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.NullString{}, nil
		}
		return sql.NullString{}, xerrors.Errorf("read audit payload row: %w", err)
	}
	if storedLength.Valid && storedLength.Int64 > maxStoredPayloadBytes {
		return sql.NullString{}, &PayloadIntegrityError{Codec: row.Codec.String, RowID: eventID, Field: column, Reason: "stored length exceeds limit"}
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
		var storedLength sql.NullInt64
		if err := q.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(body AS BLOB)) <= ? THEN body END, length(CAST(body AS BLOB)) FROM events WHERE id=?`, maxDecodedPayloadBytes, eventID).Scan(&body, &storedLength); err != nil {
			return nil, xerrors.Errorf("read legacy event payload: %w", err)
		}
		if storedLength.Valid && storedLength.Int64 > maxDecodedPayloadBytes {
			return nil, &PayloadIntegrityError{Codec: "identity", RowID: eventID, Field: "body", Reason: "stored length exceeds limit"}
		}
		return body, nil
	}
	var row payloadRow
	var storedLength sql.NullInt64
	destinations := append(row.scanDestinations(), &storedLength)
	if err := q.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(body AS BLOB)) <= ? THEN body END, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256, length(CAST(body AS BLOB)) FROM events WHERE id=?`, maxStoredPayloadBytes, eventID).Scan(destinations...); err != nil {
		return nil, xerrors.Errorf("read event payload row: %w", err)
	}
	if storedLength.Valid && storedLength.Int64 > maxStoredPayloadBytes {
		return nil, &PayloadIntegrityError{Codec: row.Codec.String, RowID: eventID, Field: "body", Reason: "stored length exceeds limit"}
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
