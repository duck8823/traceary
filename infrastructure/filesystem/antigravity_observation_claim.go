package filesystem

import (
	"context"
	"encoding/json"
	"github.com/duck8823/traceary/application"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
	"io"
	"strings"
	"unicode/utf8"
)

type antigravityObservationClaimDecoder struct{}

// NewAntigravityObservationClaimDecoder parses correlation, never native proof.
func NewAntigravityObservationClaimDecoder() application.AntigravityObservationClaimDecoder {
	return antigravityObservationClaimDecoder{}
}
func (antigravityObservationClaimDecoder) Decode(ctx context.Context, in application.AntigravityObservationClaimInput) (application.AntigravityObservationClaims, error) {
	empty := application.AntigravityObservationClaims{}
	if err := ctx.Err(); err != nil {
		return empty, xerrors.Errorf("decode Antigravity claims: %w", err)
	}
	if in.SourceVersion != "1.5.0" || in.ExpectedBoundary != domain.ObservationPromptBoundary && in.ExpectedBoundary != domain.ObservationTurnBoundary {
		return empty, nil
	}
	invalid := application.ErrInvalidAntigravityObservationClaim
	if len(in.Payload) > application.MaxAntigravityObservationClaimPayloadBytes || !utf8.ValidString(in.Payload) {
		return empty, invalid
	}
	decoder := json.NewDecoder(strings.NewReader(in.Payload))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return empty, invalid
	}
	var session string
	seen := false
	count := 0
	for decoder.More() {
		if err = ctx.Err(); err != nil {
			return empty, xerrors.Errorf("decode Antigravity claims: %w", err)
		}
		count++
		if count > 128 {
			return empty, invalid
		}
		token, err = decoder.Token()
		if err != nil {
			return empty, invalid
		}
		key, ok := token.(string)
		if !ok {
			return empty, invalid
		}
		if strings.EqualFold(key, "conversationId") {
			if key != "conversationId" || seen {
				return empty, invalid
			}
			seen = true
			token, err = decoder.Token()
			if err != nil {
				return empty, invalid
			}
			session, ok = token.(string)
			if !ok || !antigravityClaimUUID(session) {
				return empty, invalid
			}
		} else {
			for _, foreign := range []string{"session_id", "thread_id", "turn_id", "conversation_id"} {
				if strings.EqualFold(key, foreign) {
					return empty, invalid
				}
			}
			if err = skipAntigravityClaimValue(ctx, decoder, 0); err != nil {
				if ctx.Err() != nil {
					return empty, xerrors.Errorf("decode Antigravity claims: %w", ctx.Err())
				}
				return empty, invalid
			}
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return empty, invalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return empty, invalid
	}
	if err = ctx.Err(); err != nil {
		return empty, xerrors.Errorf("decode Antigravity claims: %w", err)
	}
	if !seen {
		return empty, invalid
	}
	if _, err = domain.DerivedObservationOf(domain.DerivedObservationState{Host: "antigravity", HostVersion: in.SourceVersion, Kind: in.ExpectedBoundary, EventID: "native-claim-validation", NativeSessionID: session}); err != nil {
		return empty, invalid
	}
	return application.AntigravityObservationClaims{SessionID: session, Available: true}, nil
}

// Unknown native bodies are skipped, never retained in claims or diagnostics.
func skipAntigravityClaimValue(ctx context.Context, decoder *json.Decoder, depth int) error {
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("skip Antigravity claim: %w", err)
	}
	token, err := decoder.Token()
	if err != nil {
		return application.ErrInvalidAntigravityObservationClaim
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delim != '{' && delim != '[' || depth >= 64 {
		return application.ErrInvalidAntigravityObservationClaim
	}
	for decoder.More() {
		if delim == '{' {
			key, e := decoder.Token()
			if e != nil {
				return application.ErrInvalidAntigravityObservationClaim
			}
			if _, ok := key.(string); !ok {
				return application.ErrInvalidAntigravityObservationClaim
			}
		}
		if err = skipAntigravityClaimValue(ctx, decoder, depth+1); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return application.ErrInvalidAntigravityObservationClaim
	}
	return nil
}
func antigravityClaimUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range len(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return false
		}
	}
	return true
}
