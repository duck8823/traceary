package queryservice

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// ReportQueryService loads all body-free report sources under one storage
// read snapshot. Page size and result cap have separate contracts in criteria.
type ReportQueryService interface {
	LoadReportWindow(ctx context.Context, criteria apptypes.ReportCriteria) (apptypes.ReportWindow, error)
}
