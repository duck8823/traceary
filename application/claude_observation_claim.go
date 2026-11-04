package application

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// MaxClaudeObservationClaimPayloadBytes bounds optional parsing, not primary capture.
const MaxClaudeObservationClaimPayloadBytes = 1 << 20

// ErrInvalidClaudeObservationClaim never includes native payload values.
var ErrInvalidClaudeObservationClaim = errors.New("invalid Claude observation claim")

// ClaudeObservationClaimInput borrows primary input; SourceVersion selects a
// format, not verified native provenance, generation, or current interaction.
type ClaudeObservationClaimInput struct {
	SourceVersion    string
	ExpectedBoundary domain.ObservationKind
	Payload          string
}

// ClaudeObservationClaims exposes only untrusted session correlation. Native
// prompt_id is not a turn identity. Available grants no execution authority.
type ClaudeObservationClaims struct {
	SessionID string
	Available bool
}

// ClaudeObservationClaimDecoder reads no native files and cannot bind a run.
// A nil decoder leaves optional claims unavailable without changing capture.
type ClaudeObservationClaimDecoder interface {
	Decode(context.Context, ClaudeObservationClaimInput) (ClaudeObservationClaims, error)
}
