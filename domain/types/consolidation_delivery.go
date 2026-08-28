package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// ConsolidationDelivery is how a fold request reached the agent.
type ConsolidationDelivery string

const (
	// ConsolidationDeliveryStopExit2 is stderr + exit 2 on Stop.
	ConsolidationDeliveryStopExit2 ConsolidationDelivery = "stop_exit_2"
	// ConsolidationDeliveryAdditionalContext is a next-prompt injection.
	ConsolidationDeliveryAdditionalContext ConsolidationDelivery = "additional_context"
	// ConsolidationDeliveryNone means no host channel delivered the request.
	ConsolidationDeliveryNone ConsolidationDelivery = "none"
)

// ConsolidationDeliveryFrom parses a stored delivery path.
func ConsolidationDeliveryFrom(raw string) (ConsolidationDelivery, error) {
	switch ConsolidationDelivery(strings.TrimSpace(raw)) {
	case ConsolidationDeliveryStopExit2:
		return ConsolidationDeliveryStopExit2, nil
	case ConsolidationDeliveryAdditionalContext:
		return ConsolidationDeliveryAdditionalContext, nil
	case ConsolidationDeliveryNone:
		return ConsolidationDeliveryNone, nil
	default:
		return "", xerrors.Errorf("unknown consolidation delivery %q", raw)
	}
}

// String returns the stored token.
func (d ConsolidationDelivery) String() string {
	return string(d)
}
