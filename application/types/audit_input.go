package types

import (
	domtypes "github.com/duck8823/traceary/domain/types"
)

// AuditInput carries the fields the Audit usecase records for a single
// command / tool execution. Grouping them as a named value object keeps the
// Audit call safe: every field is set by name, so the attribution strings,
// ExitCode, and Failed flag cannot be silently transposed the way a long
// positional signature allowed. The redaction policy is passed separately
// because it is a capture policy, not audit data.
type AuditInput struct {
	Command   string
	Input     string
	Output    string
	Client    domtypes.Client
	Agent     domtypes.Agent
	SessionID domtypes.SessionID
	Workspace domtypes.Workspace
	// ExitCode is the captured process exit code when a host provides one.
	ExitCode domtypes.Optional[int]
	// Failed marks a structural failure even when no numeric exit code is
	// available (e.g. Claude's PostToolUseFailure payload).
	Failed bool
	// FailureReason carries protocol-derived structured evidence. Its zero
	// value means unavailable; command payload text is never parsed as a reason.
	FailureReason domtypes.CommandFailureReason
}
