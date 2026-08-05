package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CatalogSummaryDiagnosticsReader exposes privacy-bounded aggregate evidence.
type CatalogSummaryDiagnosticsReader interface {
	CatalogSummaryDiagnostics(context.Context, apptypes.CatalogHead, apptypes.CatalogBudget) (apptypes.CatalogSummaryDiagnostics, error)
}
