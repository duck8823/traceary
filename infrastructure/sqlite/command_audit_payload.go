package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
)

// Compile-time interface assertion.
var _ queryservice.CommandAuditPayloadQueryService = (*EventDatasource)(nil)

const batchHydrateAuditSQL = `
SELECT event_id,
  CASE WHEN length(CAST(command_text AS BLOB)) <= ? THEN command_text END,
  length(CAST(command_text AS BLOB)),
  CASE WHEN length(CAST(input_text AS BLOB)) <= ? THEN input_text END,
  length(CAST(input_text AS BLOB)),
  CASE WHEN length(CAST(output_text AS BLOB)) <= ? THEN output_text END,
  length(CAST(output_text AS BLOB))
  FROM command_audits
 WHERE event_id IN (SELECT CAST(value AS TEXT) FROM json_each(?))`

// auditPayloadValues holds the stored plaintext for the three command_audits
// payload columns. A zero-value NullString means the field was not requested
// or the row had no audit record.
type auditPayloadValues struct {
	command sql.NullString
	input   sql.NullString
	output  sql.NullString
}

// HydrateCommandAudits loads selected command_audits columns onto listed
// events. Listing joins only fixed-size metadata; this is the exclusive read
// path for command_text / input_text / output_text on list and search results.
func (d *EventDatasource) HydrateCommandAudits(
	ctx context.Context,
	events []*model.Event,
	fields queryservice.CommandAuditPayloadFields,
) error {
	if d == nil || d.db == nil {
		return xerrors.Errorf("event datasource is not configured")
	}
	if !fields.Command && !fields.Input && !fields.Output {
		return nil
	}
	if len(events) == 0 {
		return nil
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for command audit payload hydration: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	// Collect event IDs that have a CommandAudit entry, preserving order.
	type auditEntry struct {
		event *model.Event
		audit *model.CommandAudit
	}
	entries := make([]auditEntry, 0, len(events))
	ids := make([]string, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		audit, ok := event.CommandAudit().Value()
		if !ok || audit == nil {
			continue
		}
		entries = append(entries, auditEntry{event, audit})
		ids = append(ids, audit.EventID().String())
	}
	if len(ids) == 0 {
		return nil
	}

	if d.onAuditHydrationQuery != nil {
		d.onAuditHydrationQuery("schema")
	}

	// Batch-fetch all payload columns for the whole id page in one SELECT.
	payloads, err := batchFetchAuditPayloads(ctx, db, ids, fields)
	if err != nil {
		return err
	}
	if d.onAuditHydrationQuery != nil {
		d.onAuditHydrationQuery("payload")
	}

	// Reconstruct domain objects from stored plaintext (no further queries).
	for _, e := range entries {
		values := payloads[e.audit.EventID().String()]
		hydrated, err := restoreCommandAuditFromValues(e.audit, fields, values)
		if err != nil {
			return xerrors.Errorf("restore hydrated command audit for %s: %w", e.audit.EventID(), err)
		}
		e.event.AttachCommandAudit(hydrated)
	}
	return nil
}

func batchFetchAuditPayloads(
	ctx context.Context,
	queryer eventSearchQueryer,
	ids []string,
	fields queryservice.CommandAuditPayloadFields,
) (map[string]auditPayloadValues, error) {
	jsonIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, xerrors.Errorf("encode audit event ID list: %w", err)
	}
	result := make(map[string]auditPayloadValues, len(ids))
	rows, err := queryer.QueryContext(ctx, batchHydrateAuditSQL,
		maxDecodedPayloadBytes, maxDecodedPayloadBytes, maxDecodedPayloadBytes, string(jsonIDs))
	if err != nil {
		return nil, xerrors.Errorf("batch fetch audit legacy payloads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var eventID string
		var cmdVal sql.NullString
		var cmdLen sql.NullInt64
		var inpVal sql.NullString
		var inpLen sql.NullInt64
		var outVal sql.NullString
		var outLen sql.NullInt64

		if err := rows.Scan(&eventID, &cmdVal, &cmdLen, &inpVal, &inpLen, &outVal, &outLen); err != nil {
			return nil, xerrors.Errorf("scan batch audit legacy row: %w", err)
		}

		var values auditPayloadValues
		if fields.Command {
			if cmdLen.Valid && cmdLen.Int64 > maxDecodedPayloadBytes {
				return nil, &PayloadIntegrityError{Codec: "identity", RowID: eventID, Field: "command", Reason: "stored length exceeds limit"}
			}
			values.command = cmdVal
		}
		if fields.Input {
			if inpLen.Valid && inpLen.Int64 > maxDecodedPayloadBytes {
				return nil, &PayloadIntegrityError{Codec: "identity", RowID: eventID, Field: "input", Reason: "stored length exceeds limit"}
			}
			values.input = inpVal
		}
		if fields.Output {
			if outLen.Valid && outLen.Int64 > maxDecodedPayloadBytes {
				return nil, &PayloadIntegrityError{Codec: "identity", RowID: eventID, Field: "output", Reason: "stored length exceeds limit"}
			}
			values.output = outVal
		}
		result[eventID] = values
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate batch audit legacy rows: %w", err)
	}
	return result, nil
}

// restoreCommandAuditFromValues rebuilds a CommandAudit from pre-decoded
// payload values. Audit metadata (wrapper, command name, truncation flags,
// etc.) is taken from the listing-time snapshot in audit.
func restoreCommandAuditFromValues(
	audit *model.CommandAudit,
	fields queryservice.CommandAuditPayloadFields,
	values auditPayloadValues,
) (*model.CommandAudit, error) {
	command := audit.Command()
	input := audit.Input()
	output := audit.Output()
	if fields.Command && values.command.Valid {
		command = values.command.String
	}
	if fields.Input && values.input.Valid {
		input = values.input.String
	}
	if fields.Output && values.output.Valid {
		output = values.output.String
	}

	snapshot := model.CommandAuditSnapshot{
		EventID:             audit.EventID(),
		Command:             command,
		Wrapper:             audit.CommandIdentity().Wrapper(),
		CommandName:         audit.CommandIdentity().Command(),
		Input:               input,
		Output:              output,
		InputTruncated:      audit.InputTruncated(),
		OutputTruncated:     audit.OutputTruncated(),
		InputOriginalBytes:  audit.InputOriginalBytes(),
		OutputOriginalBytes: audit.OutputOriginalBytes(),
		ExitCode:            audit.ExitCode(),
		Failed:              audit.Failed(),
		FailureReason:       audit.FailureReason(),
		OutputMetadata:      audit.OutputMetadata(),
	}
	if command != "" {
		restored, err := model.CommandAuditFromSnapshot(snapshot)
		if err != nil {
			return nil, xerrors.Errorf("restore hydrated command audit: %w", err)
		}
		return restored, nil
	}
	restored, err := model.CommandAuditFromListingMetadata(snapshot)
	if err != nil {
		return nil, xerrors.Errorf("restore hydrated command audit metadata: %w", err)
	}
	return restored, nil
}
