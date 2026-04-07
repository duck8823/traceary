package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// SessionID は作業セッション識別子を表す値オブジェクトです。
type SessionID string

// SessionIDOf は文字列から SessionID を生成します。
func SessionIDOf(value string) (SessionID, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return SessionID(""), xerrors.Errorf("session ID は空にできません")
	}
	return SessionID(trimmedValue), nil
}

// String は SessionID を文字列化します。
func (s SessionID) String() string { return string(s) }
