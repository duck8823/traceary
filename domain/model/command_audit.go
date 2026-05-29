package model

import (
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// CommandAudit holds detailed audit information for a command execution.
type CommandAudit struct {
	eventID         types.EventID
	command         string
	input           string
	output          string
	inputTruncated  bool
	outputTruncated bool
	inputRedacted   bool
	outputRedacted  bool
	exitCode        types.Optional[int]
	failed          bool
}

// NewCommandAudit creates a new CommandAudit.
func NewCommandAudit(
	eventID types.EventID,
	command string,
	input string,
	output string,
	inputTruncated bool,
	outputTruncated bool,
) (*CommandAudit, error) {
	trimmedCommand := strings.TrimSpace(command)
	if trimmedCommand == "" {
		return nil, xerrors.Errorf("command must not be empty")
	}

	return &CommandAudit{
		eventID:         eventID,
		command:         trimmedCommand,
		input:           input,
		output:          output,
		inputTruncated:  inputTruncated,
		outputTruncated: outputTruncated,
	}, nil
}

// CommandAuditOf restores a CommandAudit from persisted values.
func CommandAuditOf(
	eventID types.EventID,
	command string,
	input string,
	output string,
	inputTruncated bool,
	outputTruncated bool,
	exitCode types.Optional[int],
	failed bool,
) *CommandAudit {
	return &CommandAudit{
		eventID:         eventID,
		command:         command,
		input:           input,
		output:          output,
		inputTruncated:  inputTruncated,
		outputTruncated: outputTruncated,
		exitCode:        exitCode,
		failed:          failed,
	}
}

// EventID returns the linked event ID.
func (a *CommandAudit) EventID() types.EventID { return a.eventID }

// Command returns the executed command.
func (a *CommandAudit) Command() string { return a.command }

// Input returns the command input payload.
func (a *CommandAudit) Input() string { return a.input }

// Output returns the command output payload.
func (a *CommandAudit) Output() string { return a.output }

// InputTruncated reports whether input was truncated.
func (a *CommandAudit) InputTruncated() bool { return a.inputTruncated }

// OutputTruncated reports whether output was truncated.
func (a *CommandAudit) OutputTruncated() bool { return a.outputTruncated }

// SetRedaction sets whether redaction was applied during capture.
func (a *CommandAudit) SetRedaction(inputRedacted bool, outputRedacted bool) {
	if a == nil {
		return
	}

	a.inputRedacted = inputRedacted
	a.outputRedacted = outputRedacted
}

// InputRedacted reports whether input redaction was applied.
func (a *CommandAudit) InputRedacted() bool { return a.inputRedacted }

// OutputRedacted reports whether output redaction was applied.
func (a *CommandAudit) OutputRedacted() bool { return a.outputRedacted }

// ExitCode returns the exit code, or empty if not captured.
func (a *CommandAudit) ExitCode() types.Optional[int] { return a.exitCode }

// SetExitCode sets the exit code for the command.
func (a *CommandAudit) SetExitCode(code types.Optional[int]) {
	if a == nil {
		return
	}
	a.exitCode = code
}

// Failed reports whether the tool/command execution failed. This is a
// structural failure signal captured independently of exitCode, because
// some hosts (e.g. Claude Code's PostToolUseFailure payload) report failure
// without a numeric exit code in the hook payload. See docs/hooks/contract.md.
func (a *CommandAudit) Failed() bool { return a.failed }

// SetFailed marks whether the tool/command execution failed.
func (a *CommandAudit) SetFailed(failed bool) {
	if a == nil {
		return
	}
	a.failed = failed
}
