package usecase

import (
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"

	"golang.org/x/xerrors"
)

// EventDetails pairs an Event with its optional CommandAudit.
type EventDetails struct {
	event        *model.Event
	commandAudit *model.CommandAudit
}

// NewEventDetails creates an EventDetails value.
func NewEventDetails(event *model.Event, commandAudit *model.CommandAudit) (*EventDetails, error) {
	if event == nil {
		return nil, xerrors.Errorf("event must not be nil")
	}
	return &EventDetails{
		event:        event,
		commandAudit: commandAudit,
	}, nil
}

// Event returns the event.
func (d *EventDetails) Event() *model.Event { return d.event }

// CommandAudit returns the linked command audit, or nil.
func (d *EventDetails) CommandAudit() *model.CommandAudit { return d.commandAudit }

// SessionSummary holds aggregated information about a single session.
type SessionSummary struct {
	SessionID       types.SessionID
	Workspace       types.Workspace
	StartedAt       time.Time
	EndedAt         *time.Time
	Status          string
	TotalEvents     int
	CommandCount    int
	Agents          []string
	Label           string
	Summary         string
	ParentSessionID types.SessionID
}

// HandoffSummary holds information for session handoff between agents.
type HandoffSummary struct {
	SessionID      types.SessionID
	Workspace      types.Workspace
	Label          string
	Status         string
	TotalEvents    int
	CommandCount   int
	Agents         []string
	Summary        string
	RecentCommands []string
}
