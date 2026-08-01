package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CapacityInspector reads aggregate SQLite allocation metadata without reading stored values.
type CapacityInspector interface {
	InspectCapacity(ctx context.Context) (apptypes.CapacityReport, error)
}
