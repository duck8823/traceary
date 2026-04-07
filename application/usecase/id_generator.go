package usecase

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

func newEventID() (types.EventID, error) {
	value, err := newRandomHexString(16)
	if err != nil {
		return types.EventID(""), xerrors.Errorf("event ID の生成に失敗しました: %w", err)
	}

	eventID, err := types.EventIDOf(value)
	if err != nil {
		return types.EventID(""), xerrors.Errorf("event ID への変換に失敗しました: %w", err)
	}

	return eventID, nil
}

func newSessionID() (types.SessionID, error) {
	value, err := newRandomHexString(16)
	if err != nil {
		return types.SessionID(""), xerrors.Errorf("session ID の生成に失敗しました: %w", err)
	}

	sessionID, err := types.SessionIDOf("session-" + value)
	if err != nil {
		return types.SessionID(""), xerrors.Errorf("session ID への変換に失敗しました: %w", err)
	}

	return sessionID, nil
}

func newRandomHexString(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", xerrors.Errorf("乱数生成に失敗しました: %w", err)
	}

	return hex.EncodeToString(raw), nil
}
