package filesystem

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

const maxAntigravityStatusPayloadBytes = 1024 * 1024

type antigravityUsageSource struct {
	now func() time.Time
}

// NewAntigravityUsageSource creates an allowlist-only status-line decoder.
func NewAntigravityUsageSource() application.AntigravityUsageSource {
	return &antigravityUsageSource{now: time.Now}
}

type antigravityStatusPayload struct {
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id"`
	Version        string `json:"version"`
	Product        string `json:"product"`
	AgentState     string `json:"agent_state"`
	Model          struct {
		ID string `json:"id"`
	} `json:"model"`
	ContextWindow *struct {
		TotalInputTokens  *int64 `json:"total_input_tokens"`
		TotalOutputTokens *int64 `json:"total_output_tokens"`
	} `json:"context_window"`
}

func (s *antigravityUsageSource) Decode(
	_ context.Context,
	input io.Reader,
) (*application.AntigravityUsageSnapshot, error) {
	if input == nil {
		return nil, xerrors.Errorf("Antigravity status-line input is not configured")
	}
	limited := io.LimitReader(input, maxAntigravityStatusPayloadBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, xerrors.Errorf("failed to read Antigravity status-line metadata")
	}
	if len(payload) > maxAntigravityStatusPayloadBytes {
		return nil, xerrors.Errorf("Antigravity status-line metadata exceeds byte limit")
	}
	var raw antigravityStatusPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&raw); err != nil {
		return nil, xerrors.Errorf("invalid Antigravity status-line metadata")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, xerrors.Errorf("invalid trailing Antigravity status-line metadata")
	}
	if product := strings.TrimSpace(raw.Product); product != "" && product != "antigravity" {
		return nil, xerrors.Errorf("unexpected Antigravity status-line product")
	}
	if strings.TrimSpace(raw.AgentState) != "idle" {
		return nil, nil
	}
	if raw.ContextWindow == nil ||
		raw.ContextWindow.TotalInputTokens == nil ||
		raw.ContextWindow.TotalOutputTokens == nil {
		return nil, nil
	}
	if *raw.ContextWindow.TotalInputTokens < 0 || *raw.ContextWindow.TotalOutputTokens < 0 {
		return nil, xerrors.Errorf("negative Antigravity cumulative usage")
	}
	conversationID := strings.TrimSpace(raw.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(raw.SessionID)
	}
	if !validAntigravityUsageText(conversationID) {
		return nil, xerrors.Errorf("invalid Antigravity conversation identity")
	}
	sessionID, err := types.SessionIDFrom(conversationID)
	if err != nil {
		return nil, xerrors.Errorf("invalid Antigravity conversation identity")
	}
	modelName := strings.TrimSpace(raw.Model.ID)
	version := strings.TrimSpace(raw.Version)
	if !validAntigravityUsageText(modelName) || !validAntigravityUsageText(version) {
		return nil, xerrors.Errorf("invalid Antigravity status-line source identity")
	}
	return &application.AntigravityUsageSnapshot{
		ConversationID: sessionID,
		Model:          modelName,
		SourceVersion:  version,
		ObservedAt:     s.now().UTC(),
		InputTokens:    *raw.ContextWindow.TotalInputTokens,
		OutputTokens:   *raw.ContextWindow.TotalOutputTokens,
	}, nil
}

func validAntigravityUsageText(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
}
