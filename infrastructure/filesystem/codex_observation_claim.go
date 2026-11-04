package filesystem

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/duck8823/traceary/application"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

type codexObservationClaimDecoder struct{}

// NewCodexObservationClaimDecoder creates a pure, bounded format decoder. Its
// literal version selection is not native qualification or executable proof.
func NewCodexObservationClaimDecoder() application.CodexObservationClaimDecoder {
	return codexObservationClaimDecoder{}
}

func (codexObservationClaimDecoder) Decode(ctx context.Context, in application.CodexObservationClaimInput) (application.CodexObservationClaims, error) {
	if err := ctx.Err(); err != nil {
		return application.CodexObservationClaims{}, xerrors.Errorf("decode Codex claims: %w", err)
	}
	if in.SourceVersion != "codex-cli 0.166.0" {
		return application.CodexObservationClaims{}, nil
	}
	if len(in.Payload) > application.MaxCodexObservationClaimPayloadBytes || !utf8.ValidString(in.Payload) {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	decoder := json.NewDecoder(strings.NewReader(in.Payload))
	decoder.UseNumber()
	first, err := codexClaimToken(ctx, decoder)
	if err != nil {
		return application.CodexObservationClaims{}, err
	}
	if first != json.Delim('{') {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	values := make(map[string]string, 4)
	count := 0
	for decoder.More() {
		count++
		if count > 128 {
			return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
		}
		token, e := codexClaimToken(ctx, decoder)
		if e != nil {
			return application.CodexObservationClaims{}, e
		}
		key, ok := token.(string)
		if !ok {
			return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
		}
		selected := false
		for _, name := range []string{"session_id", "thread_id", "turn_id", "hook_event_name"} {
			if strings.EqualFold(key, name) {
				if key != name {
					return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
				}
				selected = true
				break
			}
		}
		if !selected {
			if e = skipCodexClaimValue(ctx, decoder, 0); e != nil {
				return application.CodexObservationClaims{}, e
			}
			continue
		}
		if _, exists := values[key]; exists {
			return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
		}
		value, e := codexClaimToken(ctx, decoder)
		if e != nil {
			return application.CodexObservationClaims{}, e
		}
		text, ok := value.(string)
		if !ok || len(text) > 256 {
			return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
		}
		values[key] = text
	}
	end, err := codexClaimToken(ctx, decoder)
	if err != nil {
		return application.CodexObservationClaims{}, err
	}
	if end != json.Delim('}') {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	if _, err = decoder.Token(); err != io.EOF {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	if err = ctx.Err(); err != nil {
		return application.CodexObservationClaims{}, xerrors.Errorf("decode Codex claims: %w", err)
	}
	session := values["session_id"]
	if session == "" || !codexClaimBoundary(values["hook_event_name"], in.ExpectedBoundary) {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	if legacy, exists := values["thread_id"]; exists && legacy != session {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	// Reuse existing bounds/lexical policy; this discarded value is validation
	// only. The actual delivered EventID remains the caller's spool receipt.
	if _, err = domain.DerivedObservationOf(domain.DerivedObservationState{Host: "codex", HostVersion: in.SourceVersion, Kind: in.ExpectedBoundary, EventID: "native-claim-validation", NativeSessionID: session, NativeTurnID: values["turn_id"]}); err != nil {
		return application.CodexObservationClaims{}, application.ErrInvalidCodexObservationClaim
	}
	return application.CodexObservationClaims{SessionID: session, TurnID: values["turn_id"], Available: true}, nil
}

func codexClaimBoundary(event string, expected domain.ObservationKind) bool {
	switch expected {
	case domain.ObservationSessionBoundary:
		return event == "SessionStart" || event == "SessionEnd"
	case domain.ObservationPromptBoundary:
		return event == "UserPromptSubmit"
	case domain.ObservationToolBoundary:
		return event == "PreToolUse" || event == "PostToolUse"
	case domain.ObservationTurnBoundary:
		return event == "Stop" || event == "Interrupt"
	default:
		return false
	}
}

func codexClaimToken(ctx context.Context, decoder *json.Decoder) (json.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("decode Codex claims: %w", err)
	}
	value, err := decoder.Token()
	if err != nil {
		return nil, application.ErrInvalidCodexObservationClaim
	}
	return value, nil
}

// Skip unknown subtrees without retaining them in results or interpreting body
// semantics/paths. Depth, payload and top-level cardinality are bounded.
func skipCodexClaimValue(ctx context.Context, decoder *json.Decoder, depth int) error {
	if depth >= 64 {
		return application.ErrInvalidCodexObservationClaim
	}
	value, err := codexClaimToken(ctx, decoder)
	if err != nil {
		return err
	}
	delim, container := value.(json.Delim)
	if !container {
		return nil
	}
	if delim != '{' && delim != '[' {
		return application.ErrInvalidCodexObservationClaim
	}
	for decoder.More() {
		if delim == '{' {
			key, e := codexClaimToken(ctx, decoder)
			if e != nil {
				return e
			}
			if _, ok := key.(string); !ok {
				return application.ErrInvalidCodexObservationClaim
			}
		}
		if err = skipCodexClaimValue(ctx, decoder, depth+1); err != nil {
			return err
		}
	}
	end, err := codexClaimToken(ctx, decoder)
	if err != nil {
		return err
	}
	if delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return application.ErrInvalidCodexObservationClaim
	}
	return nil
}
