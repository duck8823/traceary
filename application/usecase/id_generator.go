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
		return types.EventID(""), xerrors.Errorf("failed to generate event ID: %w", err)
	}

	eventID, err := types.EventIDOf(value)
	if err != nil {
		return types.EventID(""), xerrors.Errorf("failed to convert event ID: %w", err)
	}

	return eventID, nil
}

func newMemoryID() (types.MemoryID, error) {
	value, err := newRandomHexString(16)
	if err != nil {
		return types.MemoryID(""), xerrors.Errorf("failed to generate memory ID: %w", err)
	}

	memoryID, err := types.MemoryIDOf("memory-" + value)
	if err != nil {
		return types.MemoryID(""), xerrors.Errorf("failed to convert memory ID: %w", err)
	}

	return memoryID, nil
}
func newSessionID() (types.SessionID, error) {
	value, err := newRandomHexString(16)
	if err != nil {
		return types.SessionID(""), xerrors.Errorf("failed to generate session ID: %w", err)
	}

	sessionID, err := types.SessionIDOf("session-" + value)
	if err != nil {
		return types.SessionID(""), xerrors.Errorf("failed to convert session ID: %w", err)
	}

	return sessionID, nil
}

func newMemoryEdgeID() (types.MemoryEdgeID, error) {
	value, err := newRandomHexString(16)
	if err != nil {
		return types.MemoryEdgeID(""), xerrors.Errorf("failed to generate memory edge ID: %w", err)
	}

	edgeID, err := types.MemoryEdgeIDOf("edge-" + value)
	if err != nil {
		return types.MemoryEdgeID(""), xerrors.Errorf("failed to convert memory edge ID: %w", err)
	}

	return edgeID, nil
}

func newRandomHexString(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", xerrors.Errorf("failed to generate random bytes: %w", err)
	}

	return hex.EncodeToString(raw), nil
}
