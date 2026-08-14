package application

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
)

// OperatorCostWindowDays is the trailing activity window for rate and projection.
const OperatorCostWindowDays = 30

// OperatorCostInspector measures the operator's own store cost without reading bodies.
type OperatorCostInspector interface {
	InspectOperatorCost(ctx context.Context, now time.Time, residentBytes int64) (apptypes.OperatorCostReport, error)
}
