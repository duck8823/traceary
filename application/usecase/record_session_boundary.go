package usecase

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// RecordSessionBoundaryInput は session start/end の入力です。
type RecordSessionBoundaryInput struct {
	DBPath        string
	Client        string
	DefaultClient string
	Agent         string
	DefaultAgent  string
	SessionID     string
	Repo          string
	DefaultRepo   string
	Kind          types.EventKind
}

// ErrSessionStartedEventNotFound は対象 session の開始イベントが存在しないことを表します。
var ErrSessionStartedEventNotFound = xerrors.New("対象 session の開始イベントが存在しません")

// SessionStartedEventFinder は session_started イベントの取得を提供します。
type SessionStartedEventFinder interface {
	// FindSessionStartedEvent は対象 session の直近の session_started イベントを返します。
	FindSessionStartedEvent(
		ctx context.Context,
		dbPath string,
		sessionID types.SessionID,
	) (*model.Event, error)
}

// RecordSessionBoundaryUsecase は session 開始/終了イベントを記録します。
type RecordSessionBoundaryUsecase interface {
	// Run は session 境界イベントを保存します。
	Run(ctx context.Context, input RecordSessionBoundaryInput) (*model.Event, error)
}

type recordSessionBoundaryUsecase struct {
	eventSaver                EventSaver
	sessionStartedEventFinder SessionStartedEventFinder
}

// NewRecordSessionBoundaryUsecase は session 境界イベント記録ユースケースを生成します。
func NewRecordSessionBoundaryUsecase(
	eventSaver EventSaver,
	sessionStartedEventFinder SessionStartedEventFinder,
) RecordSessionBoundaryUsecase {
	return &recordSessionBoundaryUsecase{
		eventSaver:                eventSaver,
		sessionStartedEventFinder: sessionStartedEventFinder,
	}
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

	sessionID, err := resolveSessionBoundaryID(input.Kind, input.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session ID の解決に失敗しました: %w", err)
	}
	resolvedClient, resolvedAgentValue, resolvedRepo, err := u.resolveSessionBoundaryAttribution(
		ctx,
		trimmedDBPath,
		input,
		sessionID,
	)
	if err != nil {
		return nil, xerrors.Errorf("session 境界の attribution 解決に失敗しました: %w", err)
	}
	agent, err := types.AgentOf(resolvedAgentValue)
	if err != nil {
		return nil, xerrors.Errorf("agent の解決に失敗しました: %w", err)
	}
	eventID, err := newEventID()
	if err != nil {
		return nil, xerrors.Errorf("event ID の生成に失敗しました: %w", err)
	}

	event, err := model.NewEvent(
		eventID,
		input.Kind,
		resolvedClient,
		agent,
		sessionID,
		resolvedRepo,
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

func (u *recordSessionBoundaryUsecase) resolveSessionBoundaryAttribution(
	ctx context.Context,
	dbPath string,
	input RecordSessionBoundaryInput,
	sessionID types.SessionID,
) (string, string, string, error) {
	resolvedClient := strings.TrimSpace(input.Client)
	resolvedAgentValue := strings.TrimSpace(input.Agent)
	resolvedRepo := strings.TrimSpace(input.Repo)

	if input.Kind == types.EventKindSessionEnded && u.sessionStartedEventFinder != nil {
		if resolvedClient == "" || resolvedAgentValue == "" || resolvedRepo == "" {
			startedEvent, err := u.sessionStartedEventFinder.FindSessionStartedEvent(ctx, dbPath, sessionID)
			if err != nil && !errors.Is(err, ErrSessionStartedEventNotFound) {
				return "", "", "", xerrors.Errorf("session_started イベントの取得に失敗しました: %w", err)
			}
			if err == nil && startedEvent != nil {
				if resolvedClient == "" {
					resolvedClient = startedEvent.Client()
				}
				if resolvedAgentValue == "" {
					resolvedAgentValue = startedEvent.Agent().String()
				}
				if resolvedRepo == "" {
					resolvedRepo = startedEvent.Repo()
				}
			}
		}
	}

	if resolvedClient == "" {
		resolvedClient = strings.TrimSpace(input.DefaultClient)
	}
	if resolvedAgentValue == "" {
		resolvedAgentValue = strings.TrimSpace(input.DefaultAgent)
	}
	if resolvedRepo == "" {
		resolvedRepo = strings.TrimSpace(input.DefaultRepo)
	}

	return resolvedClient, resolvedAgentValue, resolvedRepo, nil
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
