package filesystem

import (
	"context"
	"encoding/json"
	"golang.org/x/xerrors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/duck8823/traceary/application"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type claudeObservationClaimDecoder struct{}

// NewClaudeObservationClaimDecoder parses optional claims, never source proof.
func NewClaudeObservationClaimDecoder() application.ClaudeObservationClaimDecoder {
	return claudeObservationClaimDecoder{}
}

func (claudeObservationClaimDecoder) Decode(ctx context.Context, in application.ClaudeObservationClaimInput) (application.ClaudeObservationClaims, error) {
	if err := ctx.Err(); err != nil {
		return application.ClaudeObservationClaims{}, xerrors.Errorf("decode Claude claims: %w", err)
	}
	if in.SourceVersion != "2.1.318 (Claude Code)" {
		return application.ClaudeObservationClaims{}, nil
	}
	invalid := application.ErrInvalidClaudeObservationClaim
	if len(in.Payload) > application.MaxClaudeObservationClaimPayloadBytes || !utf8.ValidString(in.Payload) {
		return application.ClaudeObservationClaims{}, invalid
	}
	decoder := json.NewDecoder(strings.NewReader(in.Payload))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return application.ClaudeObservationClaims{}, invalid
	}
	values := make(map[string]string, 2)
	count := 0
	for decoder.More() {
		if err = ctx.Err(); err != nil {
			return application.ClaudeObservationClaims{}, xerrors.Errorf("decode Claude claims: %w", err)
		}
		count++
		if count > 128 {
			return application.ClaudeObservationClaims{}, invalid
		}
		keyToken, e := decoder.Token()
		if e != nil {
			return application.ClaudeObservationClaims{}, invalid
		}
		key, ok := keyToken.(string)
		if !ok {
			return application.ClaudeObservationClaims{}, invalid
		}
		selected := false
		for _, name := range []string{"session_id", "hook_event_name"} {
			if strings.EqualFold(key, name) {
				if key != name {
					return application.ClaudeObservationClaims{}, invalid
				}
				selected = true
			}
		}
		// Decode/discard unknown JSON without interpreting nested claims, paths,
		// prompt_id, tool fields, descriptor hints, or purported turn identities.
		var raw json.RawMessage
		if e = decoder.Decode(&raw); e != nil {
			return application.ClaudeObservationClaims{}, invalid
		}
		if !selected {
			continue
		}
		if _, exists := values[key]; exists {
			return application.ClaudeObservationClaims{}, invalid
		}
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || len(value) > 256 {
			return application.ClaudeObservationClaims{}, invalid
		}
		values[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return application.ClaudeObservationClaims{}, invalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return application.ClaudeObservationClaims{}, invalid
	}
	if err = ctx.Err(); err != nil {
		return application.ClaudeObservationClaims{}, xerrors.Errorf("decode Claude claims: %w", err)
	}
	session := values["session_id"]
	if session == "" || !claudeClaimBoundary(values["hook_event_name"], in.ExpectedBoundary) {
		return application.ClaudeObservationClaims{}, invalid
	}
	if _, err = domain.DerivedObservationOf(domain.DerivedObservationState{Host: "claude", HostVersion: in.SourceVersion, Kind: in.ExpectedBoundary, EventID: "native-claim-validation", NativeSessionID: session}); err != nil {
		return application.ClaudeObservationClaims{}, invalid
	}
	return application.ClaudeObservationClaims{SessionID: session, Available: true}, nil
}

func claudeClaimBoundary(event string, expected domain.ObservationKind) bool {
	switch expected {
	case domain.ObservationSessionBoundary:
		return event == "SessionStart" || event == "SessionEnd"
	case domain.ObservationPromptBoundary:
		return event == "UserPromptSubmit"
	case domain.ObservationToolBoundary:
		return event == "PreToolUse" || event == "PostToolUse"
	case domain.ObservationTurnBoundary:
		return event == "Stop"
	default:
		return false
	}
}
