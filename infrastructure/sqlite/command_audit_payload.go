package sqlite

import (
	"context"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
)

// Compile-time interface assertion.
var _ queryservice.CommandAuditPayloadQueryService = (*EventDatasource)(nil)

// HydrateCommandAudits decodes selected codec-managed command_audits columns
// onto listed events. Listing joins only fixed-size metadata; this is the
// exclusive read path for command_text / input_text / output_text on list and
// search results.
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

	for _, event := range events {
		if event == nil {
			continue
		}
		audit, ok := event.CommandAudit().Value()
		if !ok || audit == nil {
			continue
		}
		hydrated, err := hydrateCommandAuditFields(ctx, db, audit, fields)
		if err != nil {
			return xerrors.Errorf("hydrate command audit payloads for %s: %w", event.EventID(), err)
		}
		event.AttachCommandAudit(hydrated)
	}
	return nil
}

func hydrateCommandAuditFields(
	ctx context.Context,
	q queryRowContexter,
	audit *model.CommandAudit,
	fields queryservice.CommandAuditPayloadFields,
) (*model.CommandAudit, error) {
	if audit == nil {
		return nil, nil
	}
	command := audit.Command()
	input := audit.Input()
	output := audit.Output()
	eventID := audit.EventID().String()

	if fields.Command {
		value, err := hydrateAuditPayload(ctx, q, eventID, "command")
		if err != nil {
			return nil, err
		}
		if value.Valid {
			command = value.String
		}
	}
	if fields.Input {
		value, err := hydrateAuditPayload(ctx, q, eventID, "input")
		if err != nil {
			return nil, err
		}
		if value.Valid {
			input = value.String
		}
	}
	if fields.Output {
		value, err := hydrateAuditPayload(ctx, q, eventID, "output")
		if err != nil {
			return nil, err
		}
		if value.Valid {
			output = value.String
		}
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
