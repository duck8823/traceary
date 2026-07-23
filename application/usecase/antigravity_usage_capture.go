package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const antigravitySnapshotRecordAttempts = 2

// AntigravityUsageStopInput identifies one observable Stop boundary whose
// provider usage is not correlatable.
type AntigravityUsageStopInput struct {
	SessionID  types.SessionID
	BoundaryID string
}

// AntigravityUsageCaptureResult exposes idempotent write outcomes.
type AntigravityUsageCaptureResult struct {
	Applied        int
	AlreadyApplied int
	Unavailable    int
}

// AntigravityUsageCaptureUsecase records idle cumulative snapshots and
// explicit unavailable Stop boundaries.
type AntigravityUsageCaptureUsecase interface {
	CaptureStatus(context.Context, io.Reader) (AntigravityUsageCaptureResult, error)
	CaptureStopUnavailable(context.Context, AntigravityUsageStopInput) (AntigravityUsageCaptureResult, error)
}

type antigravityUsageCaptureUsecase struct {
	source     application.AntigravityUsageSource
	repository application.AntigravityUsageRepository
}

// NewAntigravityUsageCaptureUsecase creates the Antigravity adapter boundary.
func NewAntigravityUsageCaptureUsecase(
	source application.AntigravityUsageSource,
	repository application.AntigravityUsageRepository,
) AntigravityUsageCaptureUsecase {
	return &antigravityUsageCaptureUsecase{source: source, repository: repository}
}

func (u *antigravityUsageCaptureUsecase) CaptureStatus(
	ctx context.Context,
	input io.Reader,
) (AntigravityUsageCaptureResult, error) {
	if u.source == nil || u.repository == nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("Antigravity usage dependencies must be configured")
	}
	snapshot, err := u.source.Decode(ctx, input)
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("failed to decode Antigravity status-line usage: %w", err)
	}
	if snapshot == nil {
		return AntigravityUsageCaptureResult{}, nil
	}
	var result AntigravityUsageCaptureResult
	for attempt := 0; attempt < antigravitySnapshotRecordAttempts; attempt++ {
		transition, err := u.recordSnapshot(ctx, *snapshot)
		if err == nil {
			countAntigravityUsageTransition(&result, transition)
			return result, nil
		}
		if !errors.Is(err, model.ErrConflictingUsageObservation) ||
			attempt+1 == antigravitySnapshotRecordAttempts {
			return result, err
		}
	}
	return result, nil
}

func (u *antigravityUsageCaptureUsecase) recordSnapshot(
	ctx context.Context,
	snapshot application.AntigravityUsageSnapshot,
) (model.UsageObservationTransition, error) {
	source, err := types.UsageSourceOf(
		"antigravity", "statusline", snapshot.SourceVersion, "google", snapshot.Model,
	)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity usage source: %w", err)
	}
	series := antigravitySnapshotSeries(snapshot)
	headOptional, err := u.repository.FindSnapshotHead(ctx, series)
	if err != nil {
		return "", xerrors.Errorf("failed to load Antigravity snapshot head: %w", err)
	}
	revision := int64(1)
	supersedes := types.None[types.UsageObservationID]()
	if head, present := headOptional.Value(); present {
		if head.Descriptor().SessionID() != snapshot.ConversationID ||
			head.Descriptor().Source() != source {
			return "", xerrors.Errorf("Antigravity snapshot head identity changed: %w", model.ErrConflictingUsageObservation)
		}
		currentInput, inputKnown := head.Counters().Input().Value()
		currentOutput, outputKnown := head.Counters().Output().Value()
		if !inputKnown || !outputKnown {
			return "", xerrors.Errorf("Antigravity snapshot head has incomplete counters: %w", model.ErrConflictingUsageObservation)
		}
		if currentInput == snapshot.InputTokens && currentOutput == snapshot.OutputTokens {
			return model.UsageObservationTransitionAlreadyApplied, nil
		}
		if snapshot.InputTokens < currentInput || snapshot.OutputTokens < currentOutput {
			return "", xerrors.Errorf("Antigravity cumulative usage regressed: %w", model.ErrConflictingUsageObservation)
		}
		revision = head.Descriptor().SnapshotRevision() + 1
		supersedes = types.Some(head.Descriptor().ObservationID())
	}
	identity := antigravitySnapshotIdentity(series, snapshot.InputTokens, snapshot.OutputTokens)
	id, err := types.UsageObservationIDFrom("antigravity:statusline:" + identity)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity snapshot identity: %w", err)
	}
	descriptor, err := model.NewUsageSnapshotDescriptor(
		id, snapshot.ConversationID, source, series, revision, supersedes, snapshot.ObservedAt.UTC(),
	)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity snapshot descriptor: %w", err)
	}
	input, err := types.KnownUsageValue(snapshot.InputTokens)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity input total: %w", err)
	}
	output, err := types.KnownUsageValue(snapshot.OutputTokens)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity output total: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		input, unavailable, unavailable, output, unavailable, unavailable,
	)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity snapshot counters: %w", err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalUnknown, snapshot.ObservedAt.UTC(),
	)
	if err != nil {
		return "", xerrors.Errorf("invalid Antigravity snapshot observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return "", xerrors.Errorf("failed to record Antigravity snapshot: %w", err)
	}
	return transition, nil
}

func (u *antigravityUsageCaptureUsecase) CaptureStopUnavailable(
	ctx context.Context,
	input AntigravityUsageStopInput,
) (AntigravityUsageCaptureResult, error) {
	if u.repository == nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("Antigravity usage repository must be configured")
	}
	if _, err := types.SessionIDFrom(input.SessionID.String()); err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("invalid Antigravity usage session: %w", err)
	}
	boundaryID := strings.TrimSpace(input.BoundaryID)
	if boundaryID == "" {
		return AntigravityUsageCaptureResult{}, nil
	}
	digest := sha256.Sum256([]byte(input.SessionID.String() + "\x00" + boundaryID))
	id, err := types.UsageObservationIDFrom("antigravity:stop_hook:" + hex.EncodeToString(digest[:]))
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("invalid Antigravity Stop usage identity: %w", err)
	}
	source, err := types.UsageSourceOf("antigravity", "stop_hook", "schema-v1", "google", "")
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("invalid Antigravity Stop usage source: %w", err)
	}
	observedAt := time.Unix(0, 0).UTC()
	descriptor, err := model.NewUsageObservationDescriptor(
		id, input.SessionID, source, types.UsageScopeCall, types.UsageAccountingExcluded, observedAt,
	)
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("invalid Antigravity Stop usage descriptor: %w", err)
	}
	unavailable := types.UnavailableUsageValue()
	counters, err := types.UsageCountersOf(
		unavailable, unavailable, unavailable, unavailable, unavailable, unavailable,
	)
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("invalid Antigravity Stop usage counters: %w", err)
	}
	observation, err := model.NewFinalizedUsageObservation(
		descriptor, counters, types.UnavailableUsageCost(), types.UsageTerminalUnknown, observedAt,
	)
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("invalid Antigravity Stop usage observation: %w", err)
	}
	transition, err := u.repository.Record(ctx, observation)
	if err != nil {
		return AntigravityUsageCaptureResult{}, xerrors.Errorf("failed to record Antigravity Stop usage: %w", err)
	}
	result := AntigravityUsageCaptureResult{Unavailable: 1}
	countAntigravityUsageTransition(&result, transition)
	if transition == model.UsageObservationTransitionAlreadyApplied {
		result.Unavailable = 0
	}
	return result, nil
}

func antigravitySnapshotSeries(snapshot application.AntigravityUsageSnapshot) string {
	digest := sha256.Sum256([]byte(
		snapshot.ConversationID.String() + "\x00" + snapshot.Model,
	))
	return "antigravity:" + hex.EncodeToString(digest[:])
}

func antigravitySnapshotIdentity(series string, input, output int64) string {
	value := series + "\x00" + strconv.FormatInt(input, 10) + "\x00" + strconv.FormatInt(output, 10)
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func countAntigravityUsageTransition(
	result *AntigravityUsageCaptureResult,
	transition model.UsageObservationTransition,
) {
	switch transition {
	case model.UsageObservationTransitionApplied:
		result.Applied++
	case model.UsageObservationTransitionAlreadyApplied:
		result.AlreadyApplied++
	}
}
