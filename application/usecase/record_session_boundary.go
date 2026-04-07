package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// RecordSessionBoundaryInput は session start/end の入力です。
type RecordSessionBoundaryInput struct {
	DBPath    string
	Client    string
	Agent     string
	SessionID string
	Repo      string
	Kind      types.EventKind
}

// RecordSessionBoundaryUsecase は session 開始/終了イベントを記録します。
type RecordSessionBoundaryUsecase interface {
	// Run は session 境界イベントを保存します。
	Run(ctx context.Context, input RecordSessionBoundaryInput) (*model.Event, error)
}

type recordSessionBoundaryUsecase struct {
	eventSaver EventSaver
}

// NewRecordSessionBoundaryUsecase は session 境界イベント記録ユースケースを生成します。
func NewRecordSessionBoundaryUsecase(eventSaver EventSaver) RecordSessionBoundaryUsecase {
	return &recordSessionBoundaryUsecase{eventSaver: eventSaver}
}

// Run は session 境界イベントを保存します。
func (u *recordSessionBoundaryUsecase) Run(
	ctx context.Context,
	input RecordSessionBoundaryInput,
) (*model.Event, error) {
	if u.eventSaver == nil {
		return nil, xerrors.Errorf("イベント保存先が設定されていません")
	}
	trimmedDBPath := strings.TrimSpace(input.DBPath)
	if trimmedDBPath == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}

	agent, err := types.AgentOf(input.Agent)
	if err != nil {
		return nil, xerrors.Errorf("agent の解決に失敗しました: %w", err)
	}
	sessionID, err := resolveSessionBoundaryID(input.Kind, input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session ID の解決に失敗しました: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("event ID の生成に失敗しました: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		input.Kind,
		strings.TrimSpace(input.Client),
		agent,
		sessionID,
		strings.TrimSpace(input.Repo),
		sessionBoundaryBody(input.Kind),
	)
	if err != nil {
		return nil, xerrors.Errorf("session 境界イベントの生成に失敗しました: %w", err)
	}
	if err := u.eventSaver.Save(ctx, trimmedDBPath, event); err != nil {
		return nil, xerrors.Errorf("session 境界イベントの保存に失敗しました: %w", err)
	}

	return event, nil
}

func resolveSessionBoundaryID(
	eventKind types.EventKind,
	sessionIDValue string,
) (types.SessionID, error) {
	switch eventKind {
	case types.EventKindSessionStarted:
		trimmedValue := strings.TrimSpace(sessionIDValue)
		if trimmedValue == "" {
			sessionID, err := newSessionID()
			if err != nil {
				return types.SessionID(""), xerrors.Errorf("session ID の生成に失敗しました: %w", err)
			}
			return sessionID, nil
		}

		sessionID, err := types.SessionIDOf(trimmedValue)
		if err != nil {
			return types.SessionID(""), xerrors.Errorf("session ID の変換に失敗しました: %w", err)
		}

		return sessionID, nil
	case types.EventKindSessionEnded:
		sessionID, err := types.SessionIDOf(sessionIDValue)
		if err != nil {
			return types.SessionID(""), xerrors.Errorf("session ID の変換に失敗しました: %w", err)
		}

		return sessionID, nil
	default:
		return types.SessionID(""), xerrors.Errorf("session 境界で扱えない event kind です: %s", eventKind)
	}
}

func sessionBoundaryBody(eventKind types.EventKind) string {
	switch eventKind {
	case types.EventKindSessionStarted:
		return "session started"
	case types.EventKindSessionEnded:
		return "session ended"
	default:
		return "session boundary"
	}
}
