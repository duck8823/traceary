package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	consolidationSkippedDisabled    = "disabled"
	consolidationSkippedSubagent    = "subagent"
	consolidationSkippedMinCommands = "min_commands"
	consolidationSkippedCadence     = "cadence"
)

type consolidationPressureUsecase struct {
	pressure   model.SessionConsolidationPressureRepository
	refinement model.SessionRefinementRepository
	kind       model.SessionKindRepository
	requests   model.ConsolidationRequestRepository
}

// NewConsolidationPressureUsecase creates the read-only stop-hook work check.
func NewConsolidationPressureUsecase(
	pressure model.SessionConsolidationPressureRepository,
	refinement model.SessionRefinementRepository,
	kind model.SessionKindRepository,
	requests model.ConsolidationRequestRepository,
) ConsolidationPressureUsecase {
	return &consolidationPressureUsecase{
		pressure:   pressure,
		refinement: refinement,
		kind:       kind,
		requests:   requests,
	}
}

func (u *consolidationPressureUsecase) Check(
	ctx context.Context,
	sessionID types.SessionID,
	policy ConsolidationPolicy,
) (ConsolidationPressureResult, error) {
	if policy.MinCommands <= 0 || policy.StopCadence <= 0 {
		return ConsolidationPressureResult{Skipped: consolidationSkippedDisabled}, nil
	}
	if u.pressure == nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("session consolidation pressure repository is not configured")
	}
	if u.refinement == nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("session refinement repository is not configured")
	}
	if u.kind == nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("session kind repository is not configured")
	}
	if u.requests == nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("consolidation request repository is not configured")
	}
	if strings.TrimSpace(sessionID.String()) == "" {
		return ConsolidationPressureResult{}, xerrors.Errorf("session id is required")
	}

	main, err := u.kind.IsMainSession(ctx, sessionID)
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to classify session: %w", err)
	}
	if isMain, ok := main.Value(); ok && !isMain {
		return ConsolidationPressureResult{Skipped: consolidationSkippedSubagent}, nil
	}

	coversTo := types.None[types.EventID]()
	previousSummary := types.None[string]()
	previousCoversTo := types.None[types.EventID]()

	existing, err := u.refinement.FindBySessionID(ctx, sessionID)
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to load session refinement: %w", err)
	}
	if row, ok := existing.Value(); ok && row != nil {
		coversTo = types.Some(row.CoversToEventID())
		previousSummary = types.Some(row.Summary())
		previousCoversTo = types.Some(row.CoversToEventID())
	}

	commands, err := u.pressure.CountKindAfter(ctx, sessionID, types.EventKindCommandExecuted, coversTo)
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to count session commands: %w", err)
	}

	result := ConsolidationPressureResult{
		Commands:         commands,
		PreviousSummary:  previousSummary,
		PreviousCoversTo: previousCoversTo,
	}
	if commands < policy.MinCommands {
		result.Skipped = consolidationSkippedMinCommands
		return result, nil
	}

	latest, err := u.requests.FindLatest(ctx, sessionID)
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to look up latest consolidation request: %w", err)
	}
	request, hasRequest := latest.Value()
	if !hasRequest {
		result.Due = true
		result.Signal = ConsolidationSignalWork
		return result, nil
	}

	stops, err := u.pressure.CountKindAfter(ctx, sessionID, types.EventKindTranscript, types.Some(request.AtEventID()))
	if err != nil {
		return ConsolidationPressureResult{}, xerrors.Errorf("failed to count session stops: %w", err)
	}
	result.Stops = stops
	if stops < policy.StopCadence {
		result.Skipped = consolidationSkippedCadence
		return result, nil
	}
	result.Due = true
	result.Signal = ConsolidationSignalWork
	return result, nil
}
