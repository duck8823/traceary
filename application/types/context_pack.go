package types

import (
	"slices"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// ContextPack is the structured working-memory bundle shared by CLI and MCP
// handoff surfaces.
type ContextPack struct {
	sessionID      domtypes.SessionID
	workspace      domtypes.Workspace
	label          string
	status         string
	totalEvents    int
	commandCount   int
	agents         []string
	workingState   WorkingState
	recentCommands []string
	memories       []MemorySummary
}

// ContextPackOf creates a ContextPack.
func ContextPackOf(
	sessionID domtypes.SessionID,
	workspace domtypes.Workspace,
	label string,
	status string,
	totalEvents int,
	commandCount int,
	agents []string,
	workingState WorkingState,
	recentCommands []string,
	memories []MemorySummary,
) ContextPack {
	return ContextPack{
		sessionID:      sessionID,
		workspace:      workspace,
		label:          label,
		status:         status,
		totalEvents:    totalEvents,
		commandCount:   commandCount,
		agents:         slices.Clone(agents),
		workingState:   workingState,
		recentCommands: slices.Clone(recentCommands),
		memories:       slices.Clone(memories),
	}
}

// SessionID returns the session ID.
func (c ContextPack) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the workspace.
func (c ContextPack) Workspace() domtypes.Workspace { return c.workspace }

// Label returns the session label.
func (c ContextPack) Label() string { return c.label }

// Status returns the session status.
func (c ContextPack) Status() string { return c.status }

// TotalEvents returns the total number of events in the session.
func (c ContextPack) TotalEvents() int { return c.totalEvents }

// CommandCount returns the total number of command events in the session.
func (c ContextPack) CommandCount() int { return c.commandCount }

// Agents returns the participating agents.
func (c ContextPack) Agents() []string { return slices.Clone(c.agents) }

// WorkingState returns the structured working-memory state.
func (c ContextPack) WorkingState() WorkingState { return c.workingState }

// RecentCommands returns recent command summaries.
func (c ContextPack) RecentCommands() []string { return slices.Clone(c.recentCommands) }

// Memories returns accepted durable memories relevant to the pack.
func (c ContextPack) Memories() []MemorySummary { return slices.Clone(c.memories) }
