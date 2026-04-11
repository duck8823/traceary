package cli

import (
	"encoding/json"
	"io"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

type eventJSON struct {
	EventID   string `json:"event_id"`
	Kind      string `json:"kind"`
	Client    string `json:"client"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	Workspace string `json:"workspace"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

type commandAuditJSON struct {
	Command         string `json:"command"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	InputTruncated  bool   `json:"input_truncated"`
	OutputTruncated bool   `json:"output_truncated"`
	ExitCode        *int   `json:"exit_code,omitempty"`
}

type eventDetailsJSON struct {
	Event        eventJSON         `json:"event"`
	CommandAudit *commandAuditJSON `json:"command_audit,omitempty"`
}

func writeEventsJSON(output io.Writer, events []*model.Event) error {
	serializedEvents := make([]eventJSON, 0, len(events))
	for _, event := range events {
		serializedEvents = append(serializedEvents, newEventJSON(event))
	}

	return writeJSON(output, serializedEvents)
}

func writeEventDetailsJSON(output io.Writer, eventDetails apptypes.EventDetails) error {
	serializedEventDetails := eventDetailsJSON{
		Event: newEventJSON(eventDetails.Event()),
	}
	auditOpt := eventDetails.CommandAudit()
	if auditOpt.IsPresent() {
		serializedEventDetails.CommandAudit = newCommandAuditJSON(auditOpt.Get())
	}

	return writeJSON(output, serializedEventDetails)
}

func writeEventJSON(output io.Writer, event *model.Event) error {
	if event == nil {
		return xerrors.Errorf(Localize("event must not be nil", "イベントは nil にできません"))
	}

	return writeJSON(output, newEventJSON(event))
}

func newEventJSON(event *model.Event) eventJSON {
	return eventJSON{
		EventID:   event.EventID().String(),
		Kind:      event.Kind().String(),
		Client:    event.Client(),
		Agent:     event.Agent().String(),
		SessionID: event.SessionID().String(),
		Workspace:      event.Workspace(),
		Message:   event.Body(),
		CreatedAt: event.CreatedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func newCommandAuditJSON(commandAudit *model.CommandAudit) *commandAuditJSON {
	if commandAudit == nil {
		return nil
	}

	return &commandAuditJSON{
		Command:         commandAudit.Command(),
		Input:           commandAudit.Input(),
		Output:          commandAudit.Output(),
		InputTruncated:  commandAudit.InputTruncated(),
		OutputTruncated: commandAudit.OutputTruncated(),
		ExitCode:        commandAudit.ExitCode(),
	}
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
