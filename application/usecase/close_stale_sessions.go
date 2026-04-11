package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/port"
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
	staleSessionCloser port.StaleSessionCloser
}

// NewCloseStaleSessionsUsecase creates a CloseStaleSessionsUsecase.
func NewCloseStaleSessionsUsecase(staleSessionCloser port.StaleSessionCloser) CloseStaleSessionsUsecase {
	return &closeStaleSessionsUsecase{staleSessionCloser: staleSessionCloser}
}

// Run executes stale session cleanup or a dry run.
func (u *closeStaleSessionsUsecase) Run(
	ctx context.Context,
	input CloseStaleSessionsInput,
) (*CloseStaleSessionsResult, error) {
	if u.staleSessionCloser == nil {
		return nil, xerrors.Errorf("stale session closer is not configured")
	}
	result, err := u.staleSessionCloser.CloseStaleSessions(
		ctx,
		port.StaleSessionCloserInput{
			StaleAfter: input.StaleAfter,
			DryRun:     input.DryRun,
		},
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to close stale sessions: %w", err)
	}

	return &CloseStaleSessionsResult{
		ClosedCount: result.ClosedCount,
	}, nil
}
