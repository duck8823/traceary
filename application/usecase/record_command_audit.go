package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	maxAuditInputLength  = 64 * 1024
	maxAuditOutputLength = 64 * 1024
)

// CommandAuditSaver はイベントとコマンド監査情報の保存を提供します。
type CommandAuditSaver interface {
	// SaveCommandAudit はイベントとコマンド監査情報を同一トランザクションで保存します。
	SaveCommandAudit(
		ctx context.Context,
		dbPath string,
		event *model.Event,
		commandAudit *model.CommandAudit,
	) error
}

// RecordCommandAuditInput は traceary audit の入力です。
type RecordCommandAuditInput struct {
	DBPath    string
	Command   string
	Input     string
	Output    string
	Client    string
	Agent     string
	SessionID string
	Repo      string
}

// RecordCommandAuditUsecase はコマンド監査イベントを保存します。
type RecordCommandAuditUsecase interface {
	// Run はコマンド監査イベントを保存します。
	Run(ctx context.Context, input RecordCommandAuditInput) (*model.Event, *model.CommandAudit, error)
}

type recordCommandAuditUsecase struct {
	commandAuditSaver CommandAuditSaver
}

// NewRecordCommandAuditUsecase はコマンド監査ユースケースを生成します。
func NewRecordCommandAuditUsecase(commandAuditSaver CommandAuditSaver) RecordCommandAuditUsecase {
	return &recordCommandAuditUsecase{commandAuditSaver: commandAuditSaver}
}

// Run はコマンド監査イベントを保存します。
func (u *recordCommandAuditUsecase) Run(
	ctx context.Context,
	input RecordCommandAuditInput,
) (*model.Event, *model.CommandAudit, error) {
	if u.commandAuditSaver == nil {
		return nil, nil, xerrors.Errorf("コマンド監査保存先が設定されていません")
	}

	trimmedDBPath := strings.TrimSpace(input.DBPath)
	if trimmedDBPath == "" {
		return nil, nil, xerrors.Errorf("DB パスは空にできません")
	}

	agent, err := types.AgentOf(input.Agent)
	if err != nil {
		return nil, nil, xerrors.Errorf("agent の解決に失敗しました: %w", err)
	}
	sessionID, err := types.SessionIDOf(input.SessionID)
	if err != nil {
		return nil, nil, xerrors.Errorf("session ID の解決に失敗しました: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, nil, xerrors.Errorf("event ID の生成に失敗しました: %w", err)
	}

	normalizedInput, inputTruncated := truncateAuditPayload(input.Input, maxAuditInputLength)
	normalizedOutput, outputTruncated := truncateAuditPayload(input.Output, maxAuditOutputLength)
	commandAudit, err := model.NewCommandAudit(
		eventID,
		input.Command,
		normalizedInput,
		normalizedOutput,
		inputTruncated,
		outputTruncated,
	)
	if err != nil {
		return nil, nil, xerrors.Errorf("コマンド監査情報の生成に失敗しました: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		types.EventKindCommandExecuted,
		strings.TrimSpace(input.Client),
		agent,
		sessionID,
		strings.TrimSpace(input.Repo),
		commandAudit.Command(),
	)
	if err != nil {
		return nil, nil, xerrors.Errorf("監査イベントの生成に失敗しました: %w", err)
	}

	if err := u.commandAuditSaver.SaveCommandAudit(ctx, trimmedDBPath, event, commandAudit); err != nil {
		return nil, nil, xerrors.Errorf("監査イベントの保存に失敗しました: %w", err)
	}

	return event, commandAudit, nil
}

func truncateAuditPayload(value string, limit int) (string, bool) {
	if limit <= 0 {
		return value, false
	}
	if len(value) <= limit {
		return value, false
	}

	const suffix = "\n...[truncated]"
	if limit <= len(suffix) {
		return suffix[:limit], true
	}

	return value[:limit-len(suffix)] + suffix, true
}
