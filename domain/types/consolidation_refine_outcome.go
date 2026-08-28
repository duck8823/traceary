package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// ConsolidationRefineOutcome is the amendment stamped onto an open request.
type ConsolidationRefineOutcome string

const (
	// ConsolidationRefineAccepted means coverage advanced (created/superseded).
	ConsolidationRefineAccepted ConsolidationRefineOutcome = "accepted"
	// ConsolidationRefineRejected means the ask was not complied with.
	ConsolidationRefineRejected ConsolidationRefineOutcome = "rejected"
)

// ConsolidationRefineOutcomeFrom parses a stored outcome token.
func ConsolidationRefineOutcomeFrom(raw string) (ConsolidationRefineOutcome, error) {
	switch ConsolidationRefineOutcome(strings.TrimSpace(raw)) {
	case ConsolidationRefineAccepted:
		return ConsolidationRefineAccepted, nil
	case ConsolidationRefineRejected:
		return ConsolidationRefineRejected, nil
	default:
		return "", xerrors.Errorf("unknown consolidation refine outcome %q", raw)
	}
}

// String returns the stored token.
func (o ConsolidationRefineOutcome) String() string {
	return string(o)
}
