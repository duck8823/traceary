package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// Event is the smallest recorded unit in Traceary history.
type Event struct {
	eventID    types.EventID
	kind       types.EventKind
	client     types.Client
	agent      types.Agent
	sessionID  types.SessionID
	workspace  types.Workspace
	body       string
	createdAt  time.Time
	sourceHook string
}

// NewEvent creates a new Event.
func NewEvent(
	eventID types.EventID,
	kind types.EventKind,
	client types.Client,
	agent types.Agent,
	sessionID types.SessionID,
	workspace types.Workspace,
	body string,
) (*Event, error) {
	return NewEventWithClock(eventID, kind, client, agent, sessionID, workspace, body, types.SystemClock{})
}

// NewEventWithClock creates a new Event using the provided clock.
func NewEventWithClock(
	eventID types.EventID,
	kind types.EventKind,
	client types.Client,
	agent types.Agent,
	sessionID types.SessionID,
	workspace types.Workspace,
	body string,
	clock types.Clock,
) (*Event, error) {
	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return nil, xerrors.Errorf("event body must not be empty")
	}
	return &Event{
		eventID:   eventID,
		kind:      kind,
		client:    client,
		agent:     agent,
		sessionID: sessionID,
		workspace: workspace,
		body:      trimmedBody,
		createdAt: clockOrSystem(clock).Now(),
	}, nil
}

// EventOf restores an Event from persisted values.
func EventOf(
	eventID types.EventID,
	kind types.EventKind,
	client types.Client,
	agent types.Agent,
	sessionID types.SessionID,
	workspace types.Workspace,
	body string,
	createdAt time.Time,
) *Event {
	return &Event{
		eventID:   eventID,
		kind:      kind,
		client:    client,
		agent:     agent,
		sessionID: sessionID,
		workspace: workspace,
		body:      body,
		createdAt: createdAt,
	}
}

// EventOfWithSourceHook restores an Event including the source_hook
// column (added in migration 000011). Legacy rows from before that
// migration keep "" here — readers that don't care about provenance
// can keep using EventOf.
func EventOfWithSourceHook(
	eventID types.EventID,
	kind types.EventKind,
	client types.Client,
	agent types.Agent,
	sessionID types.SessionID,
	workspace types.Workspace,
	body string,
	createdAt time.Time,
	sourceHook string,
) *Event {
	event := EventOf(eventID, kind, client, agent, sessionID, workspace, body, createdAt)
	event.sourceHook = sourceHook
	return event
}

// SetSourceHook stamps the host-side hook-event identifier that
// produced the event (for example "stop", "subagent_stop",
// "pre_compact", "after_agent"). Empty values are ignored so a
// caller that doesn't know / doesn't care leaves the tag unchanged.
// Called by the hook runtime layer immediately before Save.
func (e *Event) SetSourceHook(name string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return
	}
	e.sourceHook = trimmed
}

// SourceHook returns the host-side hook-event identifier set by
// SetSourceHook. Empty when the event was not produced by a hook
// (for example a `traceary log` write) or pre-dates the
// source_hook column migration.
func (e *Event) SourceHook() string { return e.sourceHook }

// EventID returns the event ID.
func (e *Event) EventID() types.EventID { return e.eventID }

// Kind returns the event kind.
func (e *Event) Kind() types.EventKind { return e.kind }

// Client returns the recording channel.
func (e *Event) Client() types.Client { return e.client }

// Agent returns the actor that produced the event.
func (e *Event) Agent() types.Agent { return e.agent }

// SessionID returns the session ID that owns the event.
func (e *Event) SessionID() types.SessionID { return e.sessionID }

// Workspace returns the auxiliary work-context identifier linked to the event.
func (e *Event) Workspace() types.Workspace { return e.workspace }

// Body returns the event body.
func (e *Event) Body() string { return e.body }

// CreatedAt returns the event creation time.
func (e *Event) CreatedAt() time.Time { return e.createdAt }
