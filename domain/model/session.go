package model

import (
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// Session represents a recorded agent work session.
type Session struct {
	sessionID       types.SessionID
	startedAt       time.Time
	endedAt         types.Optional[time.Time]
	client          types.Client
	agent           types.Agent
	workspace       types.Workspace
	label           string
	summary         string
	parentSessionID types.SessionID
	spawnEventID    types.EventID
	subagentKind    string
	spawnOrder      types.Optional[int]
}

// NewSession creates a new Session for session start.
func NewSession(
	sessionID types.SessionID,
	startedAt time.Time,
	client types.Client,
	agent types.Agent,
	workspace types.Workspace,
) *Session {
	return &Session{
		sessionID: sessionID,
		startedAt: startedAt,
		client:    client,
		agent:     agent,
		workspace: workspace,
	}
}

// NewChildSession creates a new child Session spawned from a parent session.
func NewChildSession(
	parent *Session,
	sessionID types.SessionID,
	agent types.Agent,
	workspace types.Workspace,
	spawnEventID types.EventID,
	kind string,
	order int,
) *Session {
	session := NewSession(sessionID, parent.StartedAt(), parent.Client(), agent, workspace)
	session.parentSessionID = parent.SessionID()
	session.spawnEventID = spawnEventID
	session.subagentKind = kind
	session.spawnOrder = types.Some(order)
	return session
}

// SessionOf restores a Session from persisted data.
func SessionOf(
	sessionID types.SessionID,
	startedAt time.Time,
	endedAt types.Optional[time.Time],
	client types.Client,
	agent types.Agent,
	workspace types.Workspace,
	label string,
	summary string,
	parentSessionID types.SessionID,
	spawnMetadata ...any,
) *Session {
	var (
		spawnEventID types.EventID
		subagentKind string
		spawnOrder   types.Optional[int]
	)
	if len(spawnMetadata) >= 1 {
		if value, ok := spawnMetadata[0].(types.EventID); ok {
			spawnEventID = value
		}
	}
	if len(spawnMetadata) >= 2 {
		if value, ok := spawnMetadata[1].(string); ok {
			subagentKind = value
		}
	}
	if len(spawnMetadata) >= 3 {
		if value, ok := spawnMetadata[2].(types.Optional[int]); ok {
			spawnOrder = value
		}
	}
	return &Session{
		sessionID:       sessionID,
		startedAt:       startedAt,
		endedAt:         endedAt,
		client:          client,
		agent:           agent,
		workspace:       workspace,
		label:           label,
		summary:         summary,
		parentSessionID: parentSessionID,
		spawnEventID:    spawnEventID,
		subagentKind:    subagentKind,
		spawnOrder:      spawnOrder,
	}
}

// SessionID returns the session ID.
func (s *Session) SessionID() types.SessionID { return s.sessionID }

// StartedAt returns when the session started.
func (s *Session) StartedAt() time.Time { return s.startedAt }

// EndedAt returns when the session ended, or empty if still active.
func (s *Session) EndedAt() types.Optional[time.Time] { return s.endedAt }

// Client returns the client that created the session.
func (s *Session) Client() types.Client { return s.client }

// Agent returns the agent that ran the session.
func (s *Session) Agent() types.Agent { return s.agent }

// Workspace returns the work context.
func (s *Session) Workspace() types.Workspace { return s.workspace }

// Label returns the user-assigned label.
func (s *Session) Label() string { return s.label }

// End marks the session as ended. Returns ErrInvalidSessionState when the
// session is already ended.
func (s *Session) End(endedAt time.Time, summary string) error {
	if _, ok := s.endedAt.Value(); ok {
		return ErrInvalidSessionState
	}
	s.endedAt = types.Some(endedAt)
	s.summary = summary
	return nil
}

// SetLabel updates the session label. An empty string clears the label.
func (s *Session) SetLabel(label string) { s.label = label }

// Summary returns the session summary text.
func (s *Session) Summary() string { return s.summary }

// ParentSessionID returns the parent session ID, or empty if top-level.
func (s *Session) ParentSessionID() types.SessionID { return s.parentSessionID }

// SpawnEventID returns the event that spawned this session, or empty if unknown.
func (s *Session) SpawnEventID() types.EventID { return s.spawnEventID }

// SubagentKind returns the kind of subagent spawn, or empty for top-level sessions.
func (s *Session) SubagentKind() string { return s.subagentKind }

// SpawnOrder returns this child session's sibling order when available.
func (s *Session) SpawnOrder() types.Optional[int] { return s.spawnOrder }
