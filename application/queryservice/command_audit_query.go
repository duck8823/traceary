package queryservice

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CommandAuditQueryService provides body-free, consumer-oriented audit reads.
type CommandAuditQueryService interface {
	// ListReportWindow returns every command audit matching the report event
	// criteria under one SQLite read snapshot. Limit is the internal page size.
	ListReportWindow(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.ReportCommandRecord, error)
}
