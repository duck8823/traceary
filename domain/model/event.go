package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

var nowFunc = time.Now

// Event is the smallest recorded unit in Traceary history.
type Event struct {
	eventID   types.EventID
	kind      types.EventKind
	client    string
	agent     types.Agent
	sessionID types.SessionID
	workspace string
	body      string
	createdAt time.Time
}

// NewEvent creates a new Event.
func NewEvent(
	eventID types.EventID,
	kind types.EventKind,
	client string,
	agent types.Agent,
	sessionID types.SessionID,
	workspace string,
	body string,
) (*Event, error) {
	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return nil, xerrors.Errorf("event body must not be empty")
	}
	return &Event{
		eventID:   eventID,
		kind:      kind,
		client:    strings.TrimSpace(client),
		agent:     agent,
		sessionID: sessionID,
		workspace: strings.TrimSpace(workspace),
		body:      trimmedBody,
		createdAt: nowFunc(),
	}, nil
}

// EventOf restores an Event from persisted values.
func EventOf(
	eventID types.EventID,
	kind types.EventKind,
	client string,
	agent types.Agent,
	sessionID types.SessionID,
	workspace string,
	body string,
	createdAt time.Time,
) *Event {
	return &Event{
		eventID:   eventID,
		kind:      kind,
		client:    client,
		agent:     agent,
		sessionID: sessionID,
		workspace:      workspace,
		body:      body,
		createdAt: createdAt,
	}
}

// EventID returns the event ID.
func (e *Event) EventID() types.EventID { return e.eventID }

// Kind returns the event kind.
func (e *Event) Kind() types.EventKind { return e.kind }

// Client returns the recording channel.
func (e *Event) Client() string { return e.client }

// Agent returns the actor that produced the event.
func (e *Event) Agent() types.Agent { return e.agent }

// SessionID returns the session ID that owns the event.
func (e *Event) SessionID() types.SessionID { return e.sessionID }

// Workspace returns the auxiliary work-context identifier linked to the event.
func (e *Event) Workspace() string { return e.workspace }

// Body returns the event body.
func (e *Event) Body() string { return e.body }

// CreatedAt returns the event creation time.
func (e *Event) CreatedAt() time.Time { return e.createdAt }
