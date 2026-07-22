package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// Session represents a recorded agent work session.
type Session struct {
	sessionID      types.SessionID
	startedAt      time.Time
	endedAt        types.Optional[time.Time]
	client         types.Client
	agent          types.Agent
	workspace      types.Workspace
	label          string
	summary        string
	runtimeMode    types.RuntimeMode
	terminalReason types.Optional[types.TerminalReason]
	// model is the host-reported model identifier when present. Empty means
	// the host did not report a model; Traceary never fabricates one.
	model           string
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
		sessionID:      sessionID,
		startedAt:      startedAt,
		client:         client,
		agent:          agent,
		workspace:      workspace,
		runtimeMode:    types.RuntimeModeInteractive,
		terminalReason: types.None[types.TerminalReason](),
	}
}

// NewSessionWithRuntimeMode creates an active session under an explicit
// lifecycle contract. The zero value is rejected so omission can never turn a
// one-shot runtime into an interactive runtime, or vice versa.
func NewSessionWithRuntimeMode(
	sessionID types.SessionID,
	startedAt time.Time,
	client types.Client,
	agent types.Agent,
	workspace types.Workspace,
	runtimeMode types.RuntimeMode,
) (*Session, error) {
	return NewSessionWithRuntimeModeAndParent(sessionID, startedAt, client, agent, workspace, runtimeMode, "")
}

// NewSessionWithRuntimeModeAndParent creates an active session under an
// explicit lifecycle contract and optional parent. Parent identity is part of
// construction because it is immutable after the session starts.
func NewSessionWithRuntimeModeAndParent(
	sessionID types.SessionID,
	startedAt time.Time,
	client types.Client,
	agent types.Agent,
	workspace types.Workspace,
	runtimeMode types.RuntimeMode,
	parentSessionID types.SessionID,
) (*Session, error) {
	validatedMode, err := types.RuntimeModeFrom(runtimeMode.String())
	if err != nil {
		return nil, xerrors.Errorf("invalid session runtime mode: %w", err)
	}
	if parentSessionID != "" && parentSessionID == sessionID {
		return nil, xerrors.Errorf("session cannot be its own parent: %w", ErrInvalidSessionState)
	}
	return &Session{
		sessionID:       sessionID,
		startedAt:       startedAt,
		client:          client,
		agent:           agent,
		workspace:       workspace,
		runtimeMode:     validatedMode,
		terminalReason:  types.None[types.TerminalReason](),
		parentSessionID: parentSessionID,
	}, nil
}

// NewChildSession creates a new child Session spawned from a parent session.
func NewChildSession(
	parent *Session,
	sessionID types.SessionID,
	startedAt time.Time,
	agent types.Agent,
	workspace types.Workspace,
	spawnEventID types.EventID,
	kind string,
	order int,
) *Session {
	session := NewSession(sessionID, startedAt, parent.Client(), agent, workspace)
	session.parentSessionID = parent.SessionID()
	session.spawnEventID = spawnEventID
	session.subagentKind = kind
	session.spawnOrder = types.Some(order)
	return session
}

// SessionOf restores a legacy-compatible Session from domain values. It uses
// interactive mode and maps an existing end timestamp to legacy_unknown. New
// persistence adapters that carry lifecycle fields should use
// SessionFromSnapshot instead.
// spawnMetadata may include: EventID spawnEventID, string subagentKind,
// Optional[int] spawnOrder, and optionally a trailing string model when the
// caller places model as the 4th optional value.
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
		modelName    string
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
	if len(spawnMetadata) >= 4 {
		if value, ok := spawnMetadata[3].(string); ok {
			modelName = value
		}
	}
	terminalReason := types.None[types.TerminalReason]()
	if _, ended := endedAt.Value(); ended {
		terminalReason = types.Some(types.TerminalReasonLegacyUnknown)
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
		model:           modelName,
		runtimeMode:     types.RuntimeModeInteractive,
		terminalReason:  terminalReason,
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

// RuntimeMode returns the session's explicit lifecycle contract.
func (s *Session) RuntimeMode() types.RuntimeMode { return s.runtimeMode }

// TerminalReason returns the single effective terminal reason, or empty while
// the session is active.
func (s *Session) TerminalReason() types.Optional[types.TerminalReason] {
	return s.terminalReason
}

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
	transition, err := s.Terminate(endedAt, types.TerminalReasonSuccess, summary)
	if err != nil {
		return err
	}
	if transition == SessionTerminalTransitionAlreadyApplied {
		return ErrInvalidSessionState
	}
	return nil
}

// SetLabel updates the session label. An empty string clears the label.
func (s *Session) SetLabel(label string) { s.label = label }

// Summary returns the session summary text.
func (s *Session) Summary() string { return s.summary }

// Model returns the host-reported model identifier, or empty when unavailable.
func (s *Session) Model() string {
	if s == nil {
		return ""
	}
	return s.model
}

// SetModel stores a host-reported model identifier. Empty input is ignored so
// callers can pass through missing host fields without clearing an existing
// value; use SetModelOnlyIfEmpty on the repository for persistence guards.
func (s *Session) SetModel(model string) {
	if s == nil {
		return
	}
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		s.model = trimmed
	}
}

// ParentSessionID returns the parent session ID, or empty if top-level.
func (s *Session) ParentSessionID() types.SessionID { return s.parentSessionID }

// SpawnEventID returns the event that spawned this session, or empty if unknown.
func (s *Session) SpawnEventID() types.EventID { return s.spawnEventID }

// SubagentKind returns the kind of subagent spawn, or empty for top-level sessions.
func (s *Session) SubagentKind() string { return s.subagentKind }

// SpawnOrder returns this child session's sibling order when available.
func (s *Session) SpawnOrder() types.Optional[int] { return s.spawnOrder }
