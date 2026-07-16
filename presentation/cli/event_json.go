package cli

import (
	"encoding/json"
	"io"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/sensitivepath"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

func writeEventsJSON(output io.Writer, events []*model.Event) error {
	serializedEvents := make([]event, 0, len(events))
	for _, e := range events {
		serializedEvents = append(serializedEvents, newEventOutput(e))
	}

	return writeJSON(output, serializedEvents)
}

func writeEventDetailsJSON(output io.Writer, details apptypes.EventDetails) error {
	serializedDetails := eventDetails{
		Event: newEventOutput(details.Event()),
	}
	auditOpt := details.CommandAudit()
	if audit, ok := auditOpt.Value(); ok {
		serializedDetails.CommandAudit = newCommandAuditOutput(audit)
	}

	return writeJSON(output, serializedDetails)
}

func writeEventJSON(output io.Writer, e *model.Event) error {
	if e == nil {
		return xerrors.New(Localize("event must not be nil", "イベントは nil にできません"))
	}

	return writeJSON(output, newEventOutput(e))
}

func newEventOutput(e *model.Event) event {
	return event{
		EventID:    e.EventID().String(),
		Kind:       e.Kind().String(),
		Client:     e.Client().String(),
		Agent:      e.Agent().String(),
		SessionID:  e.SessionID().String(),
		Workspace:  e.Workspace().String(),
		Message:    apptypes.ExtractPlainBody(e.Body()),
		SourceHook: e.SourceHook(),
		CreatedAt:  formatJSONTime(e.CreatedAt()),
	}
}

// newTruncatedEventOutput is the snapshot-friendly variant of
// newEventOutput. It applies the shared recent-command truncation
// policy so operator-facing list surfaces (top --snapshot --json,
// candidate failure / recent-command panes) do not balloon a single
// multi-hundred-line command_executed payload across the script's
// output. A non-positive limit disables truncation while still
// populating size metadata only when a cut actually happened — so the
// JSON shape stays additive for callers that ignore the new fields.
// Explicit detail surfaces (`traceary show`) intentionally route
// through newEventOutput so the full body remains retrievable.
func newTruncatedEventOutput(e *model.Event, limit int) event {
	base := newEventOutput(e)
	// Prefer tool-aware compact projection for large host-tool payloads so
	// list/snapshot surfaces keep auditability (path, hashes, sizes) without
	// re-emitting full Edit/Write/Read bodies. Full body remains available via
	// `traceary show` (newEventOutput).
	if summary, ok := summarizeToolAwareCommandBody(base.Message, base.EventID); ok {
		base.Message = summary
		base.Truncated = true
		base.MessageLength = utf8.RuneCountInString(apptypes.ExtractPlainBody(e.Body()))
		base.MessageBytes = len(e.Body())
		return base
	}
	result := apptypes.TruncateCommandPayload(base.Message, limit)
	base.Message = result.Body
	if result.Truncated {
		base.Truncated = true
		base.MessageLength = result.OriginalRuneCount
		base.MessageBytes = result.OriginalByteCount
	}
	return base
}

func newCommandAuditOutput(audit *model.CommandAudit) *commandAudit {
	if audit == nil {
		return nil
	}

	var exitCode *int
	if ec, ok := audit.ExitCode().Value(); ok {
		exitCode = &ec
	}
	out := &commandAudit{
		Command:             audit.Command(),
		Input:               audit.Input(),
		Output:              audit.Output(),
		InputTruncated:      audit.InputTruncated(),
		OutputTruncated:     audit.OutputTruncated(),
		InputOriginalBytes:  audit.InputOriginalBytes(),
		OutputOriginalBytes: audit.OutputOriginalBytes(),
		ExitCode:            exitCode,
		Failed:              audit.Failed(),
	}
	classification := sensitivepath.Classify(sensitivepath.Input{
		Command:         audit.Command(),
		Input:           audit.Input(),
		Output:          audit.Output(),
		InputTruncated:  audit.InputTruncated(),
		OutputTruncated: audit.OutputTruncated(),
		InputRedacted:   audit.InputRedacted(),
		OutputRedacted:  audit.OutputRedacted(),
	})
	if classification.Matched || classification.Coverage != sensitivepath.CoverageComplete {
		out.Sensitive = &sensitiveClassification{
			Matched:     classification.Matched,
			Class:       string(classification.Class),
			Operation:   string(classification.Operation),
			Evidence:    string(classification.Evidence),
			Coverage:    string(classification.Coverage),
			Redaction:   string(classification.Redaction),
			MatchedPath: classification.MatchedPath,
			IntentOnly:  classification.IntentOnly,
			Summary:     classification.Summary,
			CoverageGap: classification.CoverageGap,
		}
	}
	return out
}

func writeJSON(output io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to encode JSON", "JSON 変換に失敗しました"), err)
	}
	if _, err := output.Write(append(encoded, '\n')); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to write JSON", "JSON 出力に失敗しました"), err)
	}

	return nil
}
