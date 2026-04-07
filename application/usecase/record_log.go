package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// EventSaver はイベント保存処理を提供します。
type EventSaver interface {
	// Save は指定された DB にイベントを保存します。
	Save(ctx context.Context, dbPath string, event *model.Event) error
}

// RecordLogInput は traceary log の入力です。
type RecordLogInput struct {
	DBPath    string
	Message   string
	Client    string
	Agent     string
	SessionID string
	Repo      string
}

// RecordLogUsecase はログイベントを永続化するユースケースです。
type RecordLogUsecase interface {
	// Run はログイベントを保存します。
	Run(ctx context.Context, input RecordLogInput) (*model.Event, error)
}

type recordLogUsecase struct {
	eventSaver EventSaver
}

// NewRecordLogUsecase はログ記録ユースケースを生成します。
func NewRecordLogUsecase(eventSaver EventSaver) RecordLogUsecase {
	return &recordLogUsecase{eventSaver: eventSaver}
}

// Run はログイベントを保存します。
func (u *recordLogUsecase) Run(ctx context.Context, input RecordLogInput) (*model.Event, error) {
	if u.eventSaver == nil {
		return nil, xerrors.Errorf("イベント保存先が設定されていません")
	}
	if strings.TrimSpace(input.DBPath) == "" {
		return nil, xerrors.Errorf("DB パスは空にできません")
	}

	agent, err := types.AgentOf(input.Agent)
	if err != nil {
		return nil, xerrors.Errorf("agent の解決に失敗しました: %w", err)
	}
	sessionID, err := types.SessionIDOf(input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session ID の解決に失敗しました: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("event ID の生成に失敗しました: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		types.EventKindNote,
		strings.TrimSpace(input.Client),
		agent,
		sessionID,
		strings.TrimSpace(input.Repo),
		input.Message,
	)
	if err != nil {
		return nil, xerrors.Errorf("ログイベントの生成に失敗しました: %w", err)
	}
	if err := u.eventSaver.Save(ctx, input.DBPath, event); err != nil {
		return nil, xerrors.Errorf("ログイベントの保存に失敗しました: %w", err)
	}

	return event, nil
}

func newEventID() (types.EventID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return types.EventID(""), xerrors.Errorf("乱数生成に失敗しました: %w", err)
	}

	eventID, err := types.EventIDOf(hex.EncodeToString(raw[:]))
	if err != nil {
		return types.EventID(""), xerrors.Errorf("event ID への変換に失敗しました: %w", err)
	}

	return eventID, nil
}
