package model

import (
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// CommandAuditSnapshot is the persistence-facing state required to rehydrate
// a command audit without reparsing legacy raw text.
type CommandAuditSnapshot struct {
	EventID             types.EventID
	Command             string
	Wrapper             types.Optional[types.CommandName]
	CommandName         types.CommandName
	Input               string
	Output              string
	InputTruncated      bool
	OutputTruncated     bool
	InputOriginalBytes  int
	OutputOriginalBytes int
	ExitCode            types.Optional[int]
	Failed              bool
	FailureReason       types.CommandFailureReason
}

// CommandAuditFromSnapshot validates persisted normalized state. Empty
// command-name or failure-reason fields are accepted only as legacy unknowns.
func CommandAuditFromSnapshot(snapshot CommandAuditSnapshot) (*CommandAudit, error) {
	command := strings.TrimSpace(snapshot.Command)
	if command == "" {
		return nil, xerrors.New("restore command audit: command must not be empty")
	}
	commandName := snapshot.CommandName
	if strings.TrimSpace(commandName.String()) == "" {
		commandName = types.CommandNameUnknown
	}
	reason := snapshot.FailureReason
	if reason.String() == "" {
		reason = types.CommandFailureReasonUnknown
	}
	validatedReason, err := types.CommandFailureReasonFrom(reason.String())
	if err != nil {
		return nil, xerrors.Errorf("restore command audit failure reason: %w", err)
	}
	if code, ok := snapshot.ExitCode.Value(); ok {
		if code == 0 && validatedReason.IsFailure() {
			return nil, xerrors.New("restore command audit: exit code zero cannot be a failure")
		}
		if code != 0 && validatedReason == types.CommandFailureReasonNone {
			return nil, xerrors.New("restore command audit: non-zero exit code cannot have no failure reason")
		}
		if code != 0 {
			switch validatedReason {
			case types.CommandFailureReasonUnknown,
				types.CommandFailureReasonExitCode,
				types.CommandFailureReasonSignal,
				types.CommandFailureReasonTimeout,
				types.CommandFailureReasonHookDenied:
				// Legacy unknown and the outcomes preserved by ClassifyOutcome
				// are the only valid non-zero combinations.
			default:
				return nil, xerrors.Errorf(
					"restore command audit: non-zero exit code cannot have failure reason %s",
					validatedReason,
				)
			}
		}
	}
	if validatedReason != types.CommandFailureReasonUnknown && snapshot.Failed != validatedReason.IsFailure() {
		return nil, xerrors.New("restore command audit: failed flag contradicts structured failure reason")
	}

	failed := snapshot.Failed
	if validatedReason != types.CommandFailureReasonUnknown {
		failed = validatedReason.IsFailure()
	}
	return &CommandAudit{
		eventID:             snapshot.EventID,
		command:             command,
		commandIdentity:     types.CommandIdentityOf(snapshot.Wrapper, commandName),
		input:               snapshot.Input,
		output:              snapshot.Output,
		inputTruncated:      snapshot.InputTruncated,
		outputTruncated:     snapshot.OutputTruncated,
		inputOriginalBytes:  snapshot.InputOriginalBytes,
		outputOriginalBytes: snapshot.OutputOriginalBytes,
		exitCode:            snapshot.ExitCode,
		failed:              failed,
		failureReason:       validatedReason,
	}, nil
}
