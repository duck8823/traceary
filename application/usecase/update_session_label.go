package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// SessionLabelUpdater updates a session label.
type SessionLabelUpdater interface {
	UpdateSessionLabel(ctx context.Context, dbPath string, sessionID types.SessionID, label string) error
}

// UpdateSessionLabelInput is the input for updating a session label.
type UpdateSessionLabelInput struct {
	DBPath    string
	SessionID string
	Label     string
}

// UpdateSessionLabelUsecase updates a session's label.
type UpdateSessionLabelUsecase interface {
	Run(ctx context.Context, input UpdateSessionLabelInput) error
}

type updateSessionLabelUsecase struct {
	updater SessionLabelUpdater
}

// NewUpdateSessionLabelUsecase creates an UpdateSessionLabelUsecase.
func NewUpdateSessionLabelUsecase(updater SessionLabelUpdater) UpdateSessionLabelUsecase {
	return &updateSessionLabelUsecase{updater: updater}
}

func (u *updateSessionLabelUsecase) Run(ctx context.Context, input UpdateSessionLabelInput) error {
	if u.updater == nil {
		return xerrors.Errorf("session label updater is not configured")
	}
	trimmedDBPath := strings.TrimSpace(input.DBPath)
	if trimmedDBPath == "" {
		return xerrors.Errorf("DB path must not be empty")
	}
	trimmedSessionID := strings.TrimSpace(input.SessionID)
	if trimmedSessionID == "" {
		return xerrors.Errorf("session ID must not be empty")
	}

	sessionID, err := types.SessionIDOf(trimmedSessionID)
	if err != nil {
		return xerrors.Errorf("failed to resolve session ID: %w", err)
	}

	if err := u.updater.UpdateSessionLabel(ctx, trimmedDBPath, sessionID, input.Label); err != nil {
		return xerrors.Errorf("failed to update session label: %w", err)
	}

	return nil
}
