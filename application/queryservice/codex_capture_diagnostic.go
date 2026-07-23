package queryservice

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CodexCaptureDiagnosticQueryService loads one complete, body-free projection
// for the Codex doctor check.
type CodexCaptureDiagnosticQueryService interface {
	LoadCodexCaptureDiagnostic(
		context.Context,
		apptypes.CodexCaptureDiagnosticCriteria,
	) (apptypes.CodexCaptureDiagnosticEvidence, error)
}
