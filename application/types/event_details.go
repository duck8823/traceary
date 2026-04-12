package types

import (
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// EventDetails pairs an Event with its optional CommandAudit.
type EventDetails struct {
	event        *model.Event
	commandAudit domtypes.Optional[*model.CommandAudit]
}

// EventDetailsOf creates an EventDetails value.
func EventDetailsOf(event *model.Event, commandAudit domtypes.Optional[*model.CommandAudit]) (EventDetails, error) {
	if event == nil {
		return EventDetails{}, xerrors.Errorf("event must not be nil")
	}
	return EventDetails{
		event:        event,
		commandAudit: commandAudit,
	}, nil
}

// Event returns the event.
func (d EventDetails) Event() *model.Event { return d.event }

// CommandAudit returns the linked command audit.
func (d EventDetails) CommandAudit() domtypes.Optional[*model.CommandAudit] { return d.commandAudit }
