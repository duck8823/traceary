package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// EventID はイベント識別子を表す値オブジェクトです。
type EventID string

// EventIDOf は文字列から EventID を生成します。
func EventIDOf(value string) (EventID, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return EventID(""), xerrors.Errorf("event ID は空にできません")
	}
	return EventID(trimmedValue), nil
}

// String は EventID を文字列化します。
func (e EventID) String() string { return string(e) }
