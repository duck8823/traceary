package filesystem_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/duck8823/traceary/application"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/infrastructure/filesystem"
	"reflect"
	"strings"
	"testing"
)

const agyClaimSession = "00112233-4455-4677-8899-aabbccddeeff"

func agyClaimInput(payload string) application.AntigravityObservationClaimInput {
	return application.AntigravityObservationClaimInput{SourceVersion: "1.5.0", ExpectedBoundary: domain.ObservationPromptBoundary, Payload: payload}
}

// Synthetic format tests are not native generation or recovery certification.
func TestAntigravityObservationClaimsMeasuredBoundariesAreSessionOnly(t *testing.T) {
	for _, kind := range []domain.ObservationKind{domain.ObservationPromptBoundary, domain.ObservationTurnBoundary} {
		for _, idle := range []string{"true", "false", "null"} {
			t.Run(string(kind)+idle, func(t *testing.T) {
				in := agyClaimInput(`{"conversationId":"` + agyClaimSession + `","workspacePaths":["/PRIVATE_PATH"],"transcriptPath":"/PRIVATE_HISTORY","modelName":"PRIVATE_MODEL","fullyIdle":` + idle + `,"stepIdx":42,"invocationIdx":7,"body":{"turn_id":"PRIVATE_BODY"}}`)
				in.ExpectedBoundary = kind
				got, err := filesystem.NewAntigravityObservationClaimDecoder().Decode(context.Background(), in)
				if err != nil || got != (application.AntigravityObservationClaims{SessionID: agyClaimSession, Available: true}) {
					t.Fatal(got, err)
				}
			})
		}
	}
	if reflect.TypeFor[application.AntigravityObservationClaims]().NumField() != 2 {
		t.Fatal("claims gained non-session fields")
	}
}
func TestAntigravityObservationClaimsRejectAmbiguousAndMalformedInput(t *testing.T) {
	valid := `"conversationId":"` + agyClaimSession + `"`
	for name, payload := range map[string]string{
		"missing": `{}`, "nested_only": `{"body":{` + valid + `}}`, "null": `{"conversationId":null}`, "number": `{"conversationId":42}`,
		"blank": `{"conversationId":" "}`, "invalid_uuid": `{"conversationId":"PRIVATE_BODY"}`, "upper_uuid": `{"conversationId":"` + strings.ToUpper(agyClaimSession) + `"}`,
		"duplicate": `{` + valid + `,` + valid + `}`, "case_alias": `{"ConversationId":"PRIVATE_BODY"}`, "alias_alongside": `{` + valid + `,"conversationid":"PRIVATE_BODY"}`,
		"foreign_session": `{` + valid + `,"session_id":"PRIVATE_BODY"}`, "foreign_turn": `{` + valid + `,"turn_id":"PRIVATE_BODY"}`, "foreign_thread": `{` + valid + `,"thread_id":"PRIVATE_BODY"}`, "foreign_conversation": `{` + valid + `,"conversation_id":"PRIVATE_BODY"}`,
		"malformed": `{` + valid + `,"body":[}`, "trailing": `{` + valid + `} {}`, "array": `[]`, "utf8": `{"conversationId":"` + agyClaimSession + `","body":"` + string([]byte{0xff}) + `"}`,
		"oversize": strings.Repeat("x", application.MaxAntigravityObservationClaimPayloadBytes+1),
		"depth65":  `{` + valid + `,"body":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := filesystem.NewAntigravityObservationClaimDecoder().Decode(context.Background(), agyClaimInput(payload))
			if !errors.Is(err, application.ErrInvalidAntigravityObservationClaim) || got != (application.AntigravityObservationClaims{}) {
				t.Fatal(got, err)
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("payload leaked")
			}
		})
	}
}
func TestAntigravityObservationClaimsBoundsAndUnsupportedFormats(t *testing.T) {
	decoder := filesystem.NewAntigravityObservationClaimDecoder()
	base := `{"conversationId":"` + agyClaimSession + `"`
	for _, n := range []int{127, 128} {
		fields := base
		for i := 0; i < n; i++ {
			fields += fmt.Sprintf(`,"unknown%d":0`, i)
		}
		got, err := decoder.Decode(context.Background(), agyClaimInput(fields+`}`))
		if n == 127 {
			if err != nil || !got.Available {
				t.Fatal("128 root keys rejected", got, err)
			}
		} else if !errors.Is(err, application.ErrInvalidAntigravityObservationClaim) || got.Available {
			t.Fatal("129 root keys accepted", got, err)
		}
	}
	payload := base + `,"body":` + strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`
	if got, err := decoder.Decode(context.Background(), agyClaimInput(payload)); err != nil || !got.Available {
		t.Fatal("64 levels rejected", got, err)
	}
	prefix := base + `,"body":"`
	suffix := `"}`
	payload = prefix + strings.Repeat("a", application.MaxAntigravityObservationClaimPayloadBytes-len(prefix)-len(suffix)) + suffix
	if got, err := decoder.Decode(context.Background(), agyClaimInput(payload)); err != nil || !got.Available {
		t.Fatal("exact payload bound rejected", got, err)
	}
	for _, version := range []string{"", "1.5.1", "1.5", "agy 1.5.0", " 1.5.0"} {
		in := agyClaimInput(base + `}`)
		in.SourceVersion = version
		got, err := decoder.Decode(context.Background(), in)
		if err != nil || got != (application.AntigravityObservationClaims{}) {
			t.Fatal("version fallback", version, got, err)
		}
	}
	for _, kind := range []domain.ObservationKind{domain.ObservationSessionBoundary, domain.ObservationToolBoundary, domain.ObservationKind("usage"), domain.ObservationKind("")} {
		in := agyClaimInput(base + `}`)
		in.ExpectedBoundary = kind
		got, err := decoder.Decode(context.Background(), in)
		if err != nil || got != (application.AntigravityObservationClaims{}) {
			t.Fatal("unmeasured boundary", kind, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := decoder.Decode(ctx, agyClaimInput(base+`}`))
	if !errors.Is(err, context.Canceled) || got != (application.AntigravityObservationClaims{}) {
		t.Fatal(got, err)
	}
}
func FuzzAntigravityObservationClaims(f *testing.F) {
	f.Add(`{"conversationId":"` + agyClaimSession + `","fullyIdle":true}`)
	f.Add(`{"conversationId":null}`)
	f.Fuzz(func(t *testing.T, payload string) {
		got, err := filesystem.NewAntigravityObservationClaimDecoder().Decode(context.Background(), agyClaimInput(payload))
		if err != nil && got != (application.AntigravityObservationClaims{}) {
			t.Fatal("failed claim retained")
		}
		if got.Available && got.SessionID == "" {
			t.Fatal("empty available claim")
		}
	})
}
