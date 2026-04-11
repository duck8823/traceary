package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// SessionSummary holds aggregated information about a single session.
type SessionSummary struct {
	sessionID       domtypes.SessionID
	workspace       domtypes.Workspace
	startedAt       time.Time
	endedAt         domtypes.Optional[time.Time]
	status          string
	totalEvents     int
	commandCount    int
	agents          []string
	label           string
	summary         string
	parentSessionID domtypes.SessionID
}

// NewSessionSummary creates a SessionSummary.
func NewSessionSummary(
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
) SessionSummary {
	return SessionSummary{
		sessionID:       sessionID,
		workspace:       workspace,
		startedAt:       startedAt,
		endedAt:         endedAt,
		status:          status,
		totalEvents:     totalEvents,
		commandCount:    commandCount,
		agents:          agents,
		label:           label,
		summary:         summary,
		parentSessionID: parentSessionID,
	}
}

// SessionID returns the session ID.
func (s SessionSummary) SessionID() domtypes.SessionID { return s.sessionID }

// Workspace returns the workspace.
func (s SessionSummary) Workspace() domtypes.Workspace { return s.workspace }

// StartedAt returns when the session started.
func (s SessionSummary) StartedAt() time.Time { return s.startedAt }

// EndedAt returns when the session ended.
func (s SessionSummary) EndedAt() domtypes.Optional[time.Time] { return s.endedAt }

// Status returns the session status (active, ended, stale).
func (s SessionSummary) Status() string { return s.status }

// TotalEvents returns the total number of events in the session.
func (s SessionSummary) TotalEvents() int { return s.totalEvents }

// CommandCount returns the number of command_executed events.
func (s SessionSummary) CommandCount() int { return s.commandCount }

// Agents returns the list of agents that participated.
func (s SessionSummary) Agents() []string { return s.agents }

// Label returns the user-assigned label.
func (s SessionSummary) Label() string { return s.label }

// Summary returns the session summary text.
func (s SessionSummary) Summary() string { return s.summary }

// ParentSessionID returns the parent session ID.
func (s SessionSummary) ParentSessionID() domtypes.SessionID { return s.parentSessionID }
