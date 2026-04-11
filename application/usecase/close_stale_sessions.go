package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

// CloseStaleSessionsInput is the input for closing stale sessions.
type CloseStaleSessionsInput struct {
	StaleAfter time.Duration
	DryRun     bool
}

// CloseStaleSessionsResult is the result of a stale-session cleanup.
type CloseStaleSessionsResult struct {
	ClosedCount int
}

// CloseStaleSessionsUsecase closes sessions that have been active beyond a threshold.
type CloseStaleSessionsUsecase interface {
	Run(ctx context.Context, input CloseStaleSessionsInput) (*CloseStaleSessionsResult, error)
}

type closeStaleSessionsUsecase struct {
	storeManager application.StoreManager
}

// NewCloseStaleSessionsUsecase creates a CloseStaleSessionsUsecase.
func NewCloseStaleSessionsUsecase(storeManager application.StoreManager) CloseStaleSessionsUsecase {
	return &closeStaleSessionsUsecase{storeManager: storeManager}
}

// Run executes stale session cleanup or a dry run.
func (u *closeStaleSessionsUsecase) Run(
	ctx context.Context,
	input CloseStaleSessionsInput,
) (*CloseStaleSessionsResult, error) {
	if u.storeManager == nil {
		return nil, xerrors.Errorf("store manager is not configured")
	}
	closedCount, err := u.storeManager.CloseStaleSessions(
		ctx,
		input.StaleAfter,
		input.DryRun,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	return &CloseStaleSessionsResult{
		ClosedCount: closedCount,
	}, nil
}
