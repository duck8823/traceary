package types

import (
	"slices"
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// SessionSummary holds aggregated information about a single session.
type SessionSummary struct {
	sessionID          domtypes.SessionID
	workspace          domtypes.Workspace
	client             domtypes.Client
	startedAt          time.Time
	endedAt            domtypes.Optional[time.Time]
	status             string
	totalEvents        int
	commandCount       int
	agents             []string
	label              string
	summary            string
	parentSessionID    domtypes.SessionID
	spawnEventID       domtypes.EventID
	subagentKind       string
	spawnOrder         domtypes.Optional[int]
	latestEventAt      time.Time
	latestEventKind    domtypes.EventKind
	latestEventMessage string
}

// SessionSummaryLatestEvent carries latest-event metadata for SessionSummaryOf.
type SessionSummaryLatestEvent struct {
	Kind    domtypes.EventKind
	Message string
}

// SessionSummaryLatestEventOf creates latest-event metadata for SessionSummaryOf.
func SessionSummaryLatestEventOf(kind domtypes.EventKind, message string) SessionSummaryLatestEvent {
	return SessionSummaryLatestEvent{Kind: kind, Message: message}
}

// SessionSummaryOf creates a SessionSummary.
func SessionSummaryOf(
	sessionID domtypes.SessionID,
	workspace domtypes.Workspace,
	startedAt time.Time,
	endedAt domtypes.Optional[time.Time],
	status string,
	totalEvents int,
	commandCount int,
	agents []string,
	label string,
	summary string,
	parentSessionID domtypes.SessionID,
	spawnMetadata ...any,
) SessionSummary {
	var (
		spawnEventID       domtypes.EventID
		subagentKind       string
		spawnOrder         domtypes.Optional[int]
		latestEventAt      = startedAt
		latestEventKind    domtypes.EventKind
		latestEventMessage string
		client             domtypes.Client
	)
	for _, metadata := range spawnMetadata {
		switch value := metadata.(type) {
		case domtypes.Client:
			client = value
		case domtypes.EventID:
			spawnEventID = value
		case string:
			subagentKind = value
		case domtypes.Optional[int]:
			spawnOrder = value
		case time.Time:
			latestEventAt = value
		case SessionSummaryLatestEvent:
			latestEventKind = value.Kind
			latestEventMessage = value.Message
		}
	}
	return SessionSummary{
		sessionID:          sessionID,
		workspace:          workspace,
		client:             client,
		startedAt:          startedAt,
		endedAt:            endedAt,
		status:             status,
		totalEvents:        totalEvents,
		commandCount:       commandCount,
		agents:             slices.Clone(agents),
		label:              label,
		summary:            summary,
		parentSessionID:    parentSessionID,
		spawnEventID:       spawnEventID,
		subagentKind:       subagentKind,
		spawnOrder:         spawnOrder,
		latestEventAt:      latestEventAt,
		latestEventKind:    latestEventKind,
		latestEventMessage: latestEventMessage,
	}
}

// SessionID returns the session ID.
func (s SessionSummary) SessionID() domtypes.SessionID { return s.sessionID }

// Workspace returns the workspace.
func (s SessionSummary) Workspace() domtypes.Workspace { return s.workspace }

// Client returns the recording client.
func (s SessionSummary) Client() domtypes.Client { return s.client }

// StartedAt returns when the session started.
func (s SessionSummary) StartedAt() time.Time { return s.startedAt }

// EndedAt returns when the session ended.
func (s SessionSummary) EndedAt() domtypes.Optional[time.Time] { return s.endedAt }

// Status returns the session status (active, stale, ended, ended_with_late_events).
func (s SessionSummary) Status() string { return s.status }

// TotalEvents returns the total number of events in the session.
func (s SessionSummary) TotalEvents() int { return s.totalEvents }

// CommandCount returns the number of command_executed events.
func (s SessionSummary) CommandCount() int { return s.commandCount }

// Agents returns the list of agents that participated.
func (s SessionSummary) Agents() []string { return slices.Clone(s.agents) }

// Label returns the user-assigned label.
func (s SessionSummary) Label() string { return s.label }

// Summary returns the session summary text.
func (s SessionSummary) Summary() string { return s.summary }

// ParentSessionID returns the parent session ID.
func (s SessionSummary) ParentSessionID() domtypes.SessionID { return s.parentSessionID }

// SpawnEventID returns the event that spawned this session, or empty if unknown.
func (s SessionSummary) SpawnEventID() domtypes.EventID { return s.spawnEventID }

// SubagentKind returns the kind of subagent spawn, or empty for top-level sessions.
func (s SessionSummary) SubagentKind() string { return s.subagentKind }

// SpawnOrder returns this child session's sibling order when available.
func (s SessionSummary) SpawnOrder() domtypes.Optional[int] { return s.spawnOrder }

// LatestEventAt returns the latest recorded event timestamp in the session.
func (s SessionSummary) LatestEventAt() time.Time { return s.latestEventAt }

// LatestEventKind returns the kind of the latest recorded event in the session.
func (s SessionSummary) LatestEventKind() domtypes.EventKind { return s.latestEventKind }

// LatestEventMessage returns the plain-text message of the latest recorded event in the session.
func (s SessionSummary) LatestEventMessage() string { return s.latestEventMessage }
