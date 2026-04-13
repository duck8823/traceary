package types

import (
	"slices"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// HandoffSummary holds information for session handoff between agents.
type HandoffSummary struct {
	sessionID      domtypes.SessionID
	workspace      domtypes.Workspace
	label          string
	status         string
	totalEvents    int
	commandCount   int
	agents         []string
	summary        string
	recentCommands []string
}

// HandoffSummaryOf creates a HandoffSummary.
func HandoffSummaryOf(
	sessionID domtypes.SessionID,
	workspace domtypes.Workspace,
	label string,
	status string,
	totalEvents int,
	commandCount int,
	agents []string,
	summary string,
	recentCommands []string,
) HandoffSummary {
	return HandoffSummary{
		sessionID:      sessionID,
		workspace:      workspace,
		label:          label,
		status:         status,
		totalEvents:    totalEvents,
		commandCount:   commandCount,
		agents:         slices.Clone(agents),
		summary:        summary,
		recentCommands: slices.Clone(recentCommands),
	}
}

// SessionID returns the session ID.
func (h HandoffSummary) SessionID() domtypes.SessionID { return h.sessionID }

// Workspace returns the workspace.
func (h HandoffSummary) Workspace() domtypes.Workspace { return h.workspace }

// Label returns the session label.
func (h HandoffSummary) Label() string { return h.label }

// Status returns the session status.
func (h HandoffSummary) Status() string { return h.status }

// TotalEvents returns the total event count.
func (h HandoffSummary) TotalEvents() int { return h.totalEvents }

// CommandCount returns the command count.
func (h HandoffSummary) CommandCount() int { return h.commandCount }

// Agents returns the participating agents.
func (h HandoffSummary) Agents() []string { return slices.Clone(h.agents) }

// Summary returns the session summary text.
func (h HandoffSummary) Summary() string { return h.summary }

// RecentCommands returns recent command descriptions.
func (h HandoffSummary) RecentCommands() []string { return slices.Clone(h.recentCommands) }

// HandoffSummaryFromContextPack converts a ContextPack into the legacy
// compatibility handoff summary shape.
func HandoffSummaryFromContextPack(pack ContextPack) HandoffSummary {
	return HandoffSummaryOf(
		pack.SessionID(),
		pack.Workspace(),
		pack.Label(),
		pack.Status(),
		pack.TotalEvents(),
		pack.CommandCount(),
		pack.Agents(),
		pack.WorkingState().CombinedSummary(),
		pack.RecentCommands(),
	)
}
