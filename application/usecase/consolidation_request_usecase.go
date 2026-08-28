package usecase

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// Consolidation request reason tokens. Never err.Error() and never summary text.
const (
	ConsolidationSignalBodyBytes       = "body_bytes"
	ConsolidationReasonCreated         = "created"
	ConsolidationReasonSuperseded      = "superseded"
	ConsolidationReasonUnchanged       = "unchanged"
	ConsolidationReasonInvalidCoversTo = "invalid_covers_to"
	ConsolidationReasonUsecaseError    = "usecase_error"
)

// ConsolidationRequestInput is the measurement fact for one emitted request.
type ConsolidationRequestInput struct {
	SessionID      types.SessionID
	Client         string
	AtEventID      types.EventID
	Signal         string
	PressureValue  int64
	ThresholdValue int64
	Delivery       types.ConsolidationDelivery
}

// ConsolidationRequestRecorded is the save outcome.
type ConsolidationRequestRecorded struct {
	Recorded  bool // false = (session_id, at_event_id) already present
	ReRequest bool
}

// ConsolidationRequestUsecase records fold-request facts and refine outcomes.
type ConsolidationRequestUsecase interface {
	Record(ctx context.Context, in ConsolidationRequestInput) (ConsolidationRequestRecorded, error)
	RecordRefineOutcome(ctx context.Context, stamp model.ConsolidationRefineStamp) (bool, error)
}

// ConsolidationRefineFromSessionOutcome maps a CLI refine result to ledger tokens.
// created/superseded advance coverage → accepted; unchanged does not → rejected.
func ConsolidationRefineFromSessionOutcome(
	outcome model.SessionRefineOutcome,
) (types.ConsolidationRefineOutcome, string) {
	switch outcome {
	case model.SessionRefineOutcomeCreated:
		return types.ConsolidationRefineAccepted, ConsolidationReasonCreated
	case model.SessionRefineOutcomeSuperseded:
		return types.ConsolidationRefineAccepted, ConsolidationReasonSuperseded
	case model.SessionRefineOutcomeUnchanged:
		return types.ConsolidationRefineRejected, ConsolidationReasonUnchanged
	default:
		return types.ConsolidationRefineRejected, ConsolidationReasonUsecaseError
	}
}
