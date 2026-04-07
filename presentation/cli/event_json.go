package cli

import (
	"encoding/json"
	"io"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
)

type eventJSON struct {
	EventID   string `json:"event_id"`
	Kind      string `json:"kind"`
	Client    string `json:"client"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	Repo      string `json:"repo"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

type commandAuditJSON struct {
	Command         string `json:"command"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	InputTruncated  bool   `json:"input_truncated"`
	OutputTruncated bool   `json:"output_truncated"`
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

func writeEventDetailsJSON(output io.Writer, eventDetails *queryservice.EventDetails) error {
	if eventDetails == nil {
		return xerrors.Errorf("イベント詳細は nil にできません")
	}

	serializedEventDetails := eventDetailsJSON{
		Event: newEventJSON(eventDetails.Event()),
	}
	if eventDetails.CommandAudit() != nil {
		serializedEventDetails.CommandAudit = newCommandAuditJSON(eventDetails.CommandAudit())
	}

	return writeJSON(output, serializedEventDetails)
}

func newEventJSON(event *model.Event) eventJSON {
	return eventJSON{
		EventID:   event.EventID().String(),
		Kind:      event.Kind().String(),
		Client:    event.Client(),
		Agent:     event.Agent().String(),
		SessionID: event.SessionID().String(),
		Repo:      event.Repo(),
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
	}
}

func writeJSON(output io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return xerrors.Errorf("JSON 変換に失敗しました: %w", err)
	}
	if _, err := output.Write(append(encoded, '\n')); err != nil {
		return xerrors.Errorf("JSON 出力に失敗しました: %w", err)
	}

	return nil
}
