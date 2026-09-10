package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

type consolidationRequestUsecase struct {
	repo  model.ConsolidationRequestRepository
	clock types.Clock
}

// NewConsolidationRequestUsecase wires the ledger write port.
func NewConsolidationRequestUsecase(
	repo model.ConsolidationRequestRepository,
	clock types.Clock,
) ConsolidationRequestUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &consolidationRequestUsecase{repo: repo, clock: clock}
}

func (u *consolidationRequestUsecase) Record(
	ctx context.Context,
	in ConsolidationRequestInput,
) (ConsolidationRequestRecorded, error) {
	open, err := u.repo.FindLatestOpen(ctx, in.SessionID)
	if err != nil {
		return ConsolidationRequestRecorded{}, xerrors.Errorf("failed to look up open consolidation request: %w", err)
	}
	_, reRequest := open.Value()
	request, err := model.NewConsolidationRequest(
		in.SessionID,
		in.Client,
		u.clock.Now(),
		in.AtEventID,
		in.Signal,
		in.PressureValue,
		in.ThresholdValue,
		reRequest,
		in.Delivery,
	)
	if err != nil {
		return ConsolidationRequestRecorded{}, xerrors.Errorf("invalid consolidation request: %w", err)
	}
	recorded, err := u.repo.Save(ctx, request)
	if err != nil {
		return ConsolidationRequestRecorded{}, xerrors.Errorf("failed to save consolidation request: %w", err)
	}
	return ConsolidationRequestRecorded{Recorded: recorded, ReRequest: reRequest}, nil
}

const codexPromptLeaseDuration = 5 * time.Minute

// ClaimCodexPrompt obtains a durable, expiring delivery lease for one prompt.
func (u *consolidationRequestUsecase) ClaimCodexPrompt(ctx context.Context, sessionID types.SessionID) (types.Optional[*model.CodexPromptClaim], error) {
	token, err := newCodexPromptClaimToken()
	if err != nil {
		return types.None[*model.CodexPromptClaim](), xerrors.Errorf("failed to create Codex prompt claim token: %w", err)
	}
	now := u.clock.Now()
	claimed, err := u.repo.ClaimCodexPrompt(ctx, sessionID, token, now, now.Add(codexPromptLeaseDuration))
	if err != nil {
		return types.None[*model.CodexPromptClaim](), xerrors.Errorf("failed to claim Codex prompt consolidation: %w", err)
	}
	return claimed, nil
}

func newCodexPromptClaimToken() (types.ConsolidationPromptClaimToken, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", xerrors.Errorf("failed to generate Codex prompt claim token: %w", err)
	}
	token, err := types.ConsolidationPromptClaimTokenFrom(hex.EncodeToString(bytes))
	if err != nil {
		return "", xerrors.Errorf("failed to validate generated Codex prompt claim token: %w", err)
	}
	return token, nil
}

// ReleaseCodexPrompt makes a pre-delivery writer failure retryable.
func (u *consolidationRequestUsecase) ReleaseCodexPrompt(ctx context.Context, requestID types.ConsolidationPromptRequestID, token types.ConsolidationPromptClaimToken) error {
	if err := u.repo.ReleaseCodexPrompt(ctx, requestID, token); err != nil {
		return xerrors.Errorf("failed to release Codex prompt consolidation: %w", err)
	}
	return nil
}

// ConfirmCodexPrompt records delivery after the hook writer completed.
func (u *consolidationRequestUsecase) ConfirmCodexPrompt(ctx context.Context, requestID types.ConsolidationPromptRequestID, token types.ConsolidationPromptClaimToken) error {
	if err := u.repo.ConfirmCodexPrompt(ctx, requestID, token, u.clock.Now()); err != nil {
		return xerrors.Errorf("failed to confirm Codex prompt consolidation: %w", err)
	}
	return nil
}

func (u *consolidationRequestUsecase) RecordRefineOutcome(
	ctx context.Context,
	stamp model.ConsolidationRefineStamp,
) (bool, error) {
	if stamp.At.IsZero() {
		stamp.At = u.clock.Now()
	}
	stamped, err := u.repo.MarkRefineOutcome(ctx, stamp)
	if err != nil {
		return false, xerrors.Errorf("failed to stamp consolidation request: %w", err)
	}
	return stamped, nil
}
