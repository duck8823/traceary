package application

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// MaxAntigravityObservationClaimPayloadBytes limits optional parsing only.
const MaxAntigravityObservationClaimPayloadBytes = 1 << 20

// ErrInvalidAntigravityObservationClaim never contains native payload values.
var ErrInvalidAntigravityObservationClaim = errors.New("invalid Antigravity observation claim")

// AntigravityObservationClaimInput borrows primary input. Version and boundary
// select a format, not verified source, generation or event provenance.
type AntigravityObservationClaimInput struct {
	SourceVersion    string
	ExpectedBoundary domain.ObservationKind
	Payload          string
}

// AntigravityObservationClaims carries untrusted session correlation only.
// Available never grants live-state, permission, ownership or recovery authority.
type AntigravityObservationClaims struct {
	SessionID string
	Available bool
}

// AntigravityObservationClaimDecoder reads no native files or environment.
// A nil decoder leaves optional claims unavailable without changing capture.
type AntigravityObservationClaimDecoder interface {
	Decode(context.Context, AntigravityObservationClaimInput) (AntigravityObservationClaims, error)
}
