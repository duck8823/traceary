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

// batchHydrateAuditCodecSQL fetches codec-managed payload columns for a page
// of event IDs in one query. All three field groups are always selected so the
// SQL is static; callers skip decoding of unrequested fields at no query cost.
const batchHydrateAuditCodecSQL = `
SELECT event_id,
  CASE WHEN length(CAST(command_text AS BLOB)) <= ? THEN command_text END,
  command_codec, command_format_version, command_plaintext_bytes, command_encoded_bytes, command_sha256,
  length(CAST(command_text AS BLOB)),
  CASE WHEN length(CAST(input_text AS BLOB)) <= ? THEN input_text END,
  input_codec, input_format_version, input_plaintext_bytes, input_encoded_bytes, input_sha256,
  length(CAST(input_text AS BLOB)),
  CASE WHEN length(CAST(output_text AS BLOB)) <= ? THEN output_text END,
  output_codec, output_format_version, output_plaintext_bytes, output_encoded_bytes, output_sha256,
  length(CAST(output_text AS BLOB))
  FROM command_audits
 WHERE event_id IN (SELECT CAST(value AS TEXT) FROM json_each(?))`

// batchHydrateAuditLegacySQL fetches plain-text payload columns for a page of
// event IDs in one query (pre-codec stores). The size bound uses
// maxDecodedPayloadBytes because legacy stores use identity codec (stored == decoded).
const batchHydrateAuditLegacySQL = `
SELECT event_id,
  CASE WHEN length(CAST(command_text AS BLOB)) <= ? THEN command_text END,
  length(CAST(command_text AS BLOB)),
  CASE WHEN length(CAST(input_text AS BLOB)) <= ? THEN input_text END,
  length(CAST(input_text AS BLOB)),
  CASE WHEN length(CAST(output_text AS BLOB)) <= ? THEN output_text END,
  length(CAST(output_text AS BLOB))
  FROM command_audits
 WHERE event_id IN (SELECT CAST(value AS TEXT) FROM json_each(?))`

// auditPayloadValues holds the decoded plaintext for the three codec-managed
// command_audits columns. A zero-value NullString means the field was not
// requested or the row had no audit record.
type auditPayloadValues struct {
	command sql.NullString
	input   sql.NullString
	output  sql.NullString
}

// HydrateCommandAudits decodes selected codec-managed command_audits columns
// onto listed events. Listing joins only fixed-size metadata; this is the
// exclusive read path for command_text / input_text / output_text on list and
// search results.
//
// Query cost: O(1) schema probe + O(1) batch SELECT regardless of page size.
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

	// Schema probe: once per call, not once per field per event.
	hasCodec, err := auditHasCodecColumns(ctx, db)
	if err != nil {
		return xerrors.Errorf("inspect command_audits schema: %w", err)
	}
	if d.onAuditHydrationQuery != nil {
		d.onAuditHydrationQuery("schema")
	}

	// Batch-fetch all payload columns for the whole id page in one SELECT.
	payloads, err := batchFetchAuditPayloads(ctx, db, ids, fields, hasCodec)
	if err != nil {
		return err
	}
	if d.onAuditHydrationQuery != nil {
		d.onAuditHydrationQuery("payload")
	}

	// Reconstruct domain objects from pre-decoded values (no further queries).
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

// batchFetchAuditPayloads fetches codec-managed payload fields for all ids in
// a single SELECT using json_each. It returns a map keyed by event ID string.
// Event IDs present in ids but absent from command_audits are silently omitted.
//
// Codec / integrity / size-limit semantics are identical to hydrateAuditPayload.
func batchFetchAuditPayloads(
	ctx context.Context,
	queryer eventSearchQueryer,
	ids []string,
	fields queryservice.CommandAuditPayloadFields,
	hasCodec bool,
) (map[string]auditPayloadValues, error) {
	jsonIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, xerrors.Errorf("encode audit event ID list: %w", err)
	}
	result := make(map[string]auditPayloadValues, len(ids))
	if hasCodec {
		return batchFetchAuditCodecPayloads(ctx, queryer, string(jsonIDs), fields, result)
	}
	return batchFetchAuditLegacyPayloads(ctx, queryer, string(jsonIDs), fields, result)
}

func batchFetchAuditCodecPayloads(
	ctx context.Context,
	queryer eventSearchQueryer,
	jsonIDs string,
	fields queryservice.CommandAuditPayloadFields,
	result map[string]auditPayloadValues,
) (map[string]auditPayloadValues, error) {
	rows, err := queryer.QueryContext(ctx, batchHydrateAuditCodecSQL,
		maxStoredPayloadBytes, maxStoredPayloadBytes, maxStoredPayloadBytes, jsonIDs)
	if err != nil {
		return nil, xerrors.Errorf("batch fetch audit codec payloads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var eventID string
		var cmdRow payloadRow
		var cmdLen sql.NullInt64
		var inpRow payloadRow
		var inpLen sql.NullInt64
		var outRow payloadRow
		var outLen sql.NullInt64

		if err := rows.Scan(
			&eventID,
			&cmdRow.Stored, &cmdRow.Codec, &cmdRow.FormatVersion, &cmdRow.PlaintextBytes, &cmdRow.StoredBytes, &cmdRow.SHA256, &cmdLen,
			&inpRow.Stored, &inpRow.Codec, &inpRow.FormatVersion, &inpRow.PlaintextBytes, &inpRow.StoredBytes, &inpRow.SHA256, &inpLen,
			&outRow.Stored, &outRow.Codec, &outRow.FormatVersion, &outRow.PlaintextBytes, &outRow.StoredBytes, &outRow.SHA256, &outLen,
		); err != nil {
			return nil, xerrors.Errorf("scan batch audit codec row: %w", err)
		}

		var values auditPayloadValues
		if fields.Command {
			if cmdLen.Valid && cmdLen.Int64 > maxStoredPayloadBytes {
				return nil, &PayloadIntegrityError{Codec: cmdRow.Codec.String, RowID: eventID, Field: "command", Reason: "stored length exceeds limit"}
			}
			plain, err := cmdRow.decode(maxDecodedPayloadBytes)
			if err != nil {
				return nil, annotatePayloadError(err, eventID, "command")
			}
			values.command = sql.NullString{String: string(plain), Valid: true}
		}
		if fields.Input {
			if inpLen.Valid && inpLen.Int64 > maxStoredPayloadBytes {
				return nil, &PayloadIntegrityError{Codec: inpRow.Codec.String, RowID: eventID, Field: "input", Reason: "stored length exceeds limit"}
			}
			plain, err := inpRow.decode(maxDecodedPayloadBytes)
			if err != nil {
				return nil, annotatePayloadError(err, eventID, "input")
			}
			values.input = sql.NullString{String: string(plain), Valid: true}
		}
		if fields.Output {
			if outLen.Valid && outLen.Int64 > maxStoredPayloadBytes {
				return nil, &PayloadIntegrityError{Codec: outRow.Codec.String, RowID: eventID, Field: "output", Reason: "stored length exceeds limit"}
			}
			plain, err := outRow.decode(maxDecodedPayloadBytes)
			if err != nil {
				return nil, annotatePayloadError(err, eventID, "output")
			}
			values.output = sql.NullString{String: string(plain), Valid: true}
		}
		result[eventID] = values
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate batch audit codec rows: %w", err)
	}
	return result, nil
}

func batchFetchAuditLegacyPayloads(
	ctx context.Context,
	queryer eventSearchQueryer,
	jsonIDs string,
	fields queryservice.CommandAuditPayloadFields,
	result map[string]auditPayloadValues,
) (map[string]auditPayloadValues, error) {
	rows, err := queryer.QueryContext(ctx, batchHydrateAuditLegacySQL,
		maxDecodedPayloadBytes, maxDecodedPayloadBytes, maxDecodedPayloadBytes, jsonIDs)
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
