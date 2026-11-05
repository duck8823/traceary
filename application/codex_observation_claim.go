package application

import (
	"context"
	"errors"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// MaxCodexObservationClaimPayloadBytes bounds optional derivation, not primary capture.
const MaxCodexObservationClaimPayloadBytes = 1 << 20

// ErrInvalidCodexObservationClaim omits every native input value and body.
var ErrInvalidCodexObservationClaim = errors.New("invalid Codex observation claim")

// CodexObservationClaimInput borrows the already acquired primary payload.
// SourceVersion selects a format, never proves the installed source or binding.
type CodexObservationClaimInput struct {
	SourceVersion    string
	ExpectedBoundary domain.ObservationKind
	Payload          string
}

// CodexObservationClaims contains untrusted correlation only. Available means
// decoded, never verified native provenance, freshness, readiness or permission.
// No host event ID replaces the existing spool receipt correlation.
type CodexObservationClaims struct {
	SessionID, TurnID string
	Available         bool
}

// CodexObservationClaimDecoder enriches optional metadata without reading native
// files or granting native-current/recovery authority. Nil means unavailable.
type CodexObservationClaimDecoder interface {
	Decode(context.Context, CodexObservationClaimInput) (CodexObservationClaims, error)
}
