package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// UpdateSessionLabelInput is the input for updating a session label.
type UpdateSessionLabelInput struct {
	SessionID string
	Label     string
}

// UpdateSessionLabelUsecase updates a session's label.
type UpdateSessionLabelUsecase interface {
	Run(ctx context.Context, input UpdateSessionLabelInput) error
}

type updateSessionLabelUsecase struct {
	sessionRepo model.SessionRepository
}

// NewUpdateSessionLabelUsecase creates an UpdateSessionLabelUsecase.
func NewUpdateSessionLabelUsecase(sessionRepo model.SessionRepository) UpdateSessionLabelUsecase {
	return &updateSessionLabelUsecase{sessionRepo: sessionRepo}
}

func (u *updateSessionLabelUsecase) Run(ctx context.Context, input UpdateSessionLabelInput) error {
	if u.sessionRepo == nil {
		return xerrors.Errorf("session repository is not configured")
	}
	trimmedSessionID := strings.TrimSpace(input.SessionID)
	if trimmedSessionID == "" {
		return xerrors.Errorf("session ID must not be empty")
	}

	sessionID, err := types.SessionIDOf(trimmedSessionID)
	if err != nil {
		return xerrors.Errorf("failed to resolve session ID: %w", err)
	}

	result, err := u.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		return xerrors.Errorf("failed to find session: %w", err)
	}
	if !result.IsPresent() {
		return xerrors.Errorf("session not found: %s", sessionID)
	}

	session := result.Get()
	session.SetLabel(input.Label)

	if err := u.sessionRepo.SaveSession(ctx, session); err != nil {
		return xerrors.Errorf("failed to save session with label: %w", err)
	}

	return nil
}
