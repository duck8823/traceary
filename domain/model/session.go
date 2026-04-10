package model

import (
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// Session represents a recorded agent work session.
type Session struct {
	sessionID       types.SessionID
	startedAt       time.Time
	endedAt         *time.Time
	client          string
	agent           types.Agent
	repo            string
	label           string
	summary         string
	parentSessionID string
}

// NewSession creates a new Session for session start.
func NewSession(
	sessionID types.SessionID,
	startedAt time.Time,
	client string,
	agent types.Agent,
	repo string,
) *Session {
	return &Session{
		sessionID: sessionID,
		startedAt: startedAt,
		client:    client,
		agent:     agent,
		repo:      repo,
	}
}

// SessionOf restores a Session from persisted data.
func SessionOf(
	sessionID types.SessionID,
	startedAt time.Time,
	endedAt *time.Time,
	client string,
	agent types.Agent,
	repo string,
	label string,
	summary string,
	parentSessionID string,
) *Session {
	return &Session{
		sessionID:       sessionID,
		startedAt:       startedAt,
		endedAt:         endedAt,
		client:          client,
		agent:           agent,
		repo:            repo,
		label:           label,
		summary:         summary,
		parentSessionID: parentSessionID,
	}
}

// SessionID returns the session ID.
func (s *Session) SessionID() types.SessionID { return s.sessionID }

// StartedAt returns when the session started.
func (s *Session) StartedAt() time.Time { return s.startedAt }

// EndedAt returns when the session ended, or nil if still active.
func (s *Session) EndedAt() *time.Time { return s.endedAt }

// Client returns the client that created the session.
func (s *Session) Client() string { return s.client }

// Agent returns the agent that ran the session.
func (s *Session) Agent() types.Agent { return s.agent }

// Repo returns the work context repository.
func (s *Session) Repo() string { return s.repo }

// Label returns the user-assigned label.
func (s *Session) Label() string { return s.label }

// Summary returns the session summary text.
func (s *Session) Summary() string { return s.summary }

// ParentSessionID returns the parent session ID, or empty if top-level.
func (s *Session) ParentSessionID() string { return s.parentSessionID }
