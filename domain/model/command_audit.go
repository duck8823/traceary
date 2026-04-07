package model

import (
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// CommandAudit はコマンド実行の詳細監査情報です。
type CommandAudit struct {
	eventID         types.EventID
	command         string
	input           string
	output          string
	inputTruncated  bool
	outputTruncated bool
}

// NewCommandAudit は新しい CommandAudit を生成します。
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
		return nil, xerrors.Errorf("command は空にできません")
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

// CommandAuditOf は復元用に CommandAudit を生成します。
func CommandAuditOf(
	eventID types.EventID,
	command string,
	input string,
	output string,
	inputTruncated bool,
	outputTruncated bool,
) *CommandAudit {
	return &CommandAudit{
		eventID:         eventID,
		command:         command,
		input:           input,
		output:          output,
		inputTruncated:  inputTruncated,
		outputTruncated: outputTruncated,
	}
}

// EventID は紐づくイベント ID を返します。
func (a *CommandAudit) EventID() types.EventID { return a.eventID }

// Command は実行コマンドを返します。
func (a *CommandAudit) Command() string { return a.command }

// Input はコマンド入力を返します。
func (a *CommandAudit) Input() string { return a.input }

// Output はコマンド出力を返します。
func (a *CommandAudit) Output() string { return a.output }

// InputTruncated は入力が切り詰められたかを返します。
func (a *CommandAudit) InputTruncated() bool { return a.inputTruncated }

// OutputTruncated は出力が切り詰められたかを返します。
func (a *CommandAudit) OutputTruncated() bool { return a.outputTruncated }
