package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

// CodexCaptureDiagnosticUsecase exposes the body-free evidence required by
// doctor without leaking storage or session identity details.
type CodexCaptureDiagnosticUsecase interface {
	Load(
		context.Context,
		apptypes.CodexCaptureDiagnosticCriteria,
	) (apptypes.CodexCaptureDiagnosticEvidence, error)
}

type codexCaptureDiagnosticUsecase struct {
	query queryservice.CodexCaptureDiagnosticQueryService
}

// NewCodexCaptureDiagnosticUsecase creates the read-only diagnostic boundary.
func NewCodexCaptureDiagnosticUsecase(
	query queryservice.CodexCaptureDiagnosticQueryService,
) CodexCaptureDiagnosticUsecase {
	return &codexCaptureDiagnosticUsecase{query: query}
}

func (u *codexCaptureDiagnosticUsecase) Load(
	ctx context.Context,
	criteria apptypes.CodexCaptureDiagnosticCriteria,
) (apptypes.CodexCaptureDiagnosticEvidence, error) {
	if u.query == nil {
		return apptypes.CodexCaptureDiagnosticEvidence{}, xerrors.New("Codex capture diagnostic query service is not configured")
	}
	evidence, err := u.query.LoadCodexCaptureDiagnostic(ctx, criteria)
	if err != nil {
		return apptypes.CodexCaptureDiagnosticEvidence{}, xerrors.Errorf("failed to load Codex capture diagnostic: %w", err)
	}
	return evidence, nil
}
