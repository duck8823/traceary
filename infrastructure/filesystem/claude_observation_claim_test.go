package filesystem_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

// Synthetic parsing contracts do not qualify native generation or live state.
func TestClaudeObservationClaimsPreserveOnlyUntrustedSession(t *testing.T) {
	decoder := filesystem.NewClaudeObservationClaimDecoder()
	for event, kind := range map[string]domain.ObservationKind{
		"SessionStart": domain.ObservationSessionBoundary, "SessionEnd": domain.ObservationSessionBoundary,
		"UserPromptSubmit": domain.ObservationPromptBoundary, "PreToolUse": domain.ObservationToolBoundary,
		"PostToolUse": domain.ObservationToolBoundary, "Stop": domain.ObservationTurnBoundary,
	} {
		t.Run(event, func(t *testing.T) {
			in := application.ClaudeObservationClaimInput{SourceVersion: "2.1.318 (Claude Code)", ExpectedBoundary: kind,
				Payload: `{"hook_event_name":"` + event + `","session_id":"native-session","prompt_id":"not-a-turn","turn_id":"foreign-turn","prompt":"PRIVATE_BODY","tool_input":{"session_id":"foreign"}}`}
			got, err := decoder.Decode(context.Background(), in)
			if err != nil || !got.Available || got.SessionID != "native-session" {
				t.Fatal(got, err)
			}
		})
	}
	typ := reflect.TypeFor[application.ClaudeObservationClaims]()
	if typ.NumField() != 2 {
		t.Fatal("claims gained body/turn/generation/authority", typ)
	}
}

func TestClaudeObservationClaimsFailClosedWithoutBodyInErrors(t *testing.T) {
	for name, payload := range map[string]string{
		"duplicate":       `{"session_id":"a","session_id":"PRIVATE_BODY","hook_event_name":"UserPromptSubmit"}`,
		"alias":           `{"Session_ID":"PRIVATE_BODY","hook_event_name":"UserPromptSubmit"}`,
		"unicode_alias":   `{"ſession_id":"PRIVATE_BODY","hook_event_name":"UserPromptSubmit"}`,
		"event_alias":     `{"session_id":"a","Hook_Event_Name":"UserPromptSubmit"}`,
		"duplicate_event": `{"session_id":"a","hook_event_name":"UserPromptSubmit","hook_event_name":"UserPromptSubmit"}`,
		"missing":         `{"hook_event_name":"UserPromptSubmit"}`,
		"null":            `{"session_id":null,"hook_event_name":"UserPromptSubmit"}`,
		"number":          `{"session_id":12,"hook_event_name":"UserPromptSubmit"}`,
		"blank":           `{"session_id":" ","hook_event_name":"UserPromptSubmit"}`,
		"wrong_event":     `{"session_id":"a","hook_event_name":"Stop"}`,
		"permission":      `{"session_id":"a","hook_event_name":"PermissionRequest"}`,
		"trailing":        `{"session_id":"a","hook_event_name":"UserPromptSubmit"} {}`,
		"nested_only":     `{"body":{"session_id":"PRIVATE_BODY"},"hook_event_name":"UserPromptSubmit"}`,
		"malformed":       `{"session_id":"a","hook_event_name":"UserPromptSubmit","body":[}`,
		"array":           `[]`,
		"oversize":        strings.Repeat("x", application.MaxClaudeObservationClaimPayloadBytes+1),
		"invalid_utf8":    "{\"session_id\":\"a\",\"hook_event_name\":\"UserPromptSubmit\",\"body\":\"\xff\"}",
		"many_fields":     `{"session_id":"a","hook_event_name":"UserPromptSubmit",` + strings.Repeat(`"body":0,`, 128) + `"last":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := filesystem.NewClaudeObservationClaimDecoder().Decode(context.Background(), application.ClaudeObservationClaimInput{SourceVersion: "2.1.318 (Claude Code)", ExpectedBoundary: domain.ObservationPromptBoundary, Payload: payload})
			if !errors.Is(err, application.ErrInvalidClaudeObservationClaim) || got.Available || got.SessionID != "" {
				t.Fatal(got, err)
			}
			if strings.Contains(err.Error(), "PRIVATE_BODY") {
				t.Fatal("body leaked")
			}
		})
	}
}

func TestClaudeObservationClaimsUnknownVersionAndCancellation(t *testing.T) {
	decoder := filesystem.NewClaudeObservationClaimDecoder()
	got, err := decoder.Decode(context.Background(), application.ClaudeObservationClaimInput{SourceVersion: "2.1.319 (Claude Code)", Payload: "not json"})
	if err != nil || got.Available {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = decoder.Decode(ctx, application.ClaudeObservationClaimInput{SourceVersion: "2.1.318 (Claude Code)"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
