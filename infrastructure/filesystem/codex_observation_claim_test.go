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

// Synthetic contract cases are not native Codex 0.166.0 qualification.
func TestCodexObservationClaimsDecodeCanonicalMetadataOnly(t *testing.T) {
	decoder := filesystem.NewCodexObservationClaimDecoder()
	input := application.CodexObservationClaimInput{SourceVersion: "codex-cli 0.166.0", ExpectedBoundary: domain.ObservationPromptBoundary, Payload: `{"hook_event_name":"UserPromptSubmit","session_id":"native-session","turn_id":"native-turn","prompt":"PRIVATE_BODY","tool_input":{"session_id":"foreign-body"},"transcript_path":"/not/read"}`}
	got, err := decoder.Decode(context.Background(), input)
	if err != nil || !got.Available || got.SessionID != "native-session" || got.TurnID != "native-turn" {
		t.Fatal(got, err)
	}
	typ := reflect.TypeFor[application.CodexObservationClaims]()
	if typ.NumField() != 3 {
		t.Fatal("claim result grew into another body/authority/event DTO", typ)
	}
	for i := 0; i < typ.NumField(); i++ {
		if !strings.Contains("SessionID TurnID Available", typ.Field(i).Name) {
			t.Fatal(typ.Field(i).Name)
		}
	}
}

func TestCodexObservationClaimsFailClosedWithoutLeakingPayload(t *testing.T) {
	for _, tt := range []struct{ name, payload string }{
		{"duplicate_session", `{"hook_event_name":"UserPromptSubmit","session_id":"A","session_id":"PRIVATE_BODY"}`},
		{"duplicate_turn", `{"hook_event_name":"UserPromptSubmit","session_id":"A","turn_id":"x","turn_id":"PRIVATE_BODY"}`},
		{"legacy_only", `{"hook_event_name":"UserPromptSubmit","thread_id":"PRIVATE_BODY"}`},
		{"conflicting_alias", `{"hook_event_name":"UserPromptSubmit","session_id":"A","thread_id":"PRIVATE_BODY"}`},
		{"duplicate_legacy", `{"hook_event_name":"UserPromptSubmit","session_id":"A","thread_id":"A","thread_id":"PRIVATE_BODY"}`},
		{"case_alias", `{"hook_event_name":"UserPromptSubmit","Session_ID":"PRIVATE_BODY"}`},
		{"unicode_alias", `{"hook_event_name":"UserPromptSubmit","session_id":"A","ſession_id":"PRIVATE_BODY"}`},
		{"null_session", `{"hook_event_name":"UserPromptSubmit","session_id":null}`},
		{"object_session", `{"hook_event_name":"UserPromptSubmit","session_id":{"private":"PRIVATE_BODY"}}`},
		{"null_turn", `{"hook_event_name":"UserPromptSubmit","session_id":"A","turn_id":null}`},
		{"blank_session", `{"hook_event_name":"UserPromptSubmit","session_id":" "}`},
		{"wrong_event", `{"hook_event_name":"Stop","session_id":"A"}`},
		{"missing_event", `{"session_id":"A"}`},
		{"trailing_value", `{"hook_event_name":"UserPromptSubmit","session_id":"A"} {"private":"PRIVATE_BODY"}`},
		{"malformed_tail", `{"hook_event_name":"UserPromptSubmit","session_id":"A","private":[}`},
		{"non_object", `["PRIVATE_BODY"]`},
		{"invalid_utf8_body", "{\"hook_event_name\":\"UserPromptSubmit\",\"session_id\":\"A\",\"body\":\"\xff\"}"},
		{"long_identity", `{"hook_event_name":"UserPromptSubmit","session_id":"` + strings.Repeat("A", 257) + `"}`},
		{"oversize", strings.Repeat(" ", application.MaxCodexObservationClaimPayloadBytes+1)},
		{"deep_body", `{"hook_event_name":"UserPromptSubmit","session_id":"A","private":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filesystem.NewCodexObservationClaimDecoder().Decode(context.Background(), application.CodexObservationClaimInput{SourceVersion: "codex-cli 0.166.0", ExpectedBoundary: domain.ObservationPromptBoundary, Payload: tt.payload})
			if got.Available || got.SessionID != "" || got.TurnID != "" {
				t.Fatal("ambiguous claims escaped", got)
			}
			if !errors.Is(err, application.ErrInvalidCodexObservationClaim) {
				t.Fatal("malformed payload lost fixed invalid-claim error", err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE_BODY") {
				t.Fatal("private body leaked in error")
			}
		})
	}
}

func TestCodexObservationClaimsExactVersionAndEventBoundary(t *testing.T) {
	decoder := filesystem.NewCodexObservationClaimDecoder()
	for _, version := range []string{"", "0.166.0", "codex-cli 0.166.1", "Codex-CLI 0.166.0", "codex-cli 0.145.0"} {
		got, err := decoder.Decode(context.Background(), application.CodexObservationClaimInput{SourceVersion: version, ExpectedBoundary: domain.ObservationPromptBoundary, Payload: `{"hook_event_name":"UserPromptSubmit","session_id":"A"}`})
		if err != nil || got.Available {
			t.Fatal("unknown version promoted", version, got, err)
		}
	}
	for _, tt := range []struct {
		event    string
		boundary domain.ObservationKind
	}{{"SessionStart", domain.ObservationSessionBoundary}, {"SessionEnd", domain.ObservationSessionBoundary}, {"UserPromptSubmit", domain.ObservationPromptBoundary}, {"PostToolUse", domain.ObservationToolBoundary}, {"Stop", domain.ObservationTurnBoundary}} {
		got, err := decoder.Decode(context.Background(), application.CodexObservationClaimInput{SourceVersion: "codex-cli 0.166.0", ExpectedBoundary: tt.boundary, Payload: `{"hook_event_name":"` + tt.event + `","session_id":"A"}`})
		if err != nil || !got.Available || got.TurnID != "" {
			t.Fatal(tt, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := decoder.Decode(ctx, application.CodexObservationClaimInput{SourceVersion: "codex-cli 0.166.0"}); !errors.Is(err, context.Canceled) || got.Available {
		t.Fatal(got, err)
	}
}

func FuzzCodexObservationClaimDecoderNeverRetainsBody(f *testing.F) {
	f.Add(`{"hook_event_name":"UserPromptSubmit","session_id":"A","turn_id":"B"}`)
	f.Add(`{"session_id":"A","session_id":"B"}`)
	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > application.MaxCodexObservationClaimPayloadBytes+1 {
			return
		}
		got, err := filesystem.NewCodexObservationClaimDecoder().Decode(context.Background(), application.CodexObservationClaimInput{SourceVersion: "codex-cli 0.166.0", ExpectedBoundary: domain.ObservationPromptBoundary, Payload: payload})
		if !got.Available && (got.SessionID != "" || got.TurnID != "") {
			t.Fatal("partial claims escaped")
		}
		if len(got.SessionID) > 256 || len(got.TurnID) > 256 {
			t.Fatal("unbounded claims")
		}
		if err != nil && err.Error() != "invalid Codex observation claim" {
			t.Fatal("nonconstant parse failure", err)
		}
	})
}
