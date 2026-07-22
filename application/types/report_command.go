package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// ReportCommandRecord is the body-free read model used to aggregate command
// audit outcomes for a report.
type ReportCommandRecord struct {
	EventID       domtypes.EventID
	Client        domtypes.Client
	Agent         domtypes.Agent
	SessionID     domtypes.SessionID
	Workspace     domtypes.Workspace
	Wrapper       domtypes.Optional[domtypes.CommandName]
	CommandName   domtypes.CommandName
	ExitCode      domtypes.Optional[int]
	Failed        bool
	FailureReason domtypes.CommandFailureReason
	CreatedAt     time.Time
}

// IsFailure applies the compatibility rule for report aggregation. An
// explicit zero exit code is always success; known reasons are authoritative;
// legacy unknown rows fall back to their persisted failed/non-zero evidence.
func (r ReportCommandRecord) IsFailure() bool {
	if code, ok := r.ExitCode.Value(); ok {
		if code == 0 {
			return false
		}
	}
	if r.FailureReason != domtypes.CommandFailureReasonUnknown && r.FailureReason.String() != "" {
		return r.FailureReason.IsFailure()
	}
	if code, ok := r.ExitCode.Value(); ok && code != 0 {
		return true
	}
	return r.Failed
}

// EffectiveFailureReason returns a reportable reason without inferring from
// text. Legacy non-zero exit codes can be identified structurally; other
// legacy failures remain unknown.
func (r ReportCommandRecord) EffectiveFailureReason() domtypes.CommandFailureReason {
	if code, ok := r.ExitCode.Value(); ok {
		if code == 0 {
			return domtypes.CommandFailureReasonNone
		}
		if r.FailureReason == domtypes.CommandFailureReasonUnknown || r.FailureReason.String() == "" {
			return domtypes.CommandFailureReasonExitCode
		}
	}
	if r.FailureReason.String() == "" {
		return domtypes.CommandFailureReasonUnknown
	}
	return r.FailureReason
}

// ReportCommandRow contains one normalized executable aggregate.
type ReportCommandRow struct {
	Command       string
	Count         int
	FailedCount   int
	FailureRate   float64
	SampleEventID string
}

// ReportFailureLoop identifies a repeated failure for one command identity.
type ReportFailureLoop struct {
	Command        string
	Workspace      string
	Agent          string
	Count          int
	SampleEventIDs []string
}

// ReportCommandSummary is the body-free command portion of a report.
type ReportCommandSummary struct {
	FailureTotal     int
	FailuresByClient map[string]int
	FailuresByReason map[string]int
	FailureSamples   []string
	TopCommands      []ReportCommandRow
	FailureLoops     []ReportFailureLoop
}
