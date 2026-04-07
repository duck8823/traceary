package types

import (
	"slices"
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

var knownEventKinds = []EventKind{
	EventKindNote,
	EventKindCommandExecuted,
	EventKindReviewed,
	EventKindSessionStarted,
	EventKindSessionEnded,
}

// EventKindOf は文字列から EventKind を生成します。
func EventKindOf(value string) (EventKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EventKind(""), xerrors.Errorf("event kind は空にできません")
	}

	if slices.Contains(knownEventKinds, EventKind(trimmedValue)) {
		return EventKind(trimmedValue), nil
	}

	return EventKind(""), xerrors.Errorf(
		"未知の event kind です: %s (有効な値: %s)",
		trimmedValue,
		strings.Join(KnownEventKindStrings(), ", "),
	)
}

// String は EventKind を文字列化します。
func (e EventKind) String() string { return string(e) }

// KnownEventKindStrings は既知の event kind 一覧を返します。
func KnownEventKindStrings() []string {
	values := make([]string, 0, len(knownEventKinds))
	for _, kind := range knownEventKinds {
		values = append(values, kind.String())
	}

	return values
}
