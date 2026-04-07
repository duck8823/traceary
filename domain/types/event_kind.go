package types

import (
	"strings"

	"golang.org/x/xerrors"
)

const (
	// EventKindNote はメモ系イベントを表します。
	EventKindNote EventKind = "note"
	// EventKindCommandExecuted はコマンド実行イベントを表します。
	EventKindCommandExecuted EventKind = "command_executed"
	// EventKindReviewed はレビューイベントを表します。
	EventKindReviewed EventKind = "reviewed"
	// EventKindSessionStarted はセッション開始イベントを表します。
	EventKindSessionStarted EventKind = "session_started"
	// EventKindSessionEnded はセッション終了イベントを表します。
	EventKindSessionEnded EventKind = "session_ended"
)

// EventKind はイベント種別を表す値オブジェクトです。
type EventKind string

// EventKindOf は文字列から EventKind を生成します。
func EventKindOf(value string) (EventKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EventKind(""), xerrors.Errorf("event kind は空にできません")
	}

	switch EventKind(trimmedValue) {
	case EventKindNote,
		EventKindCommandExecuted,
		EventKindReviewed,
		EventKindSessionStarted,
		EventKindSessionEnded:
		return EventKind(trimmedValue), nil
	default:
		return EventKind(""), xerrors.Errorf("未知の event kind です: %s", trimmedValue)
	}
}

// String は EventKind を文字列化します。
func (e EventKind) String() string { return string(e) }
