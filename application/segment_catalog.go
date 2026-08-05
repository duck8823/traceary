package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CatalogInventoryGateReader proves archive-sequence readiness without exposing private keys.
type CatalogInventoryGateReader interface {
	CatalogInventoryGate(context.Context, apptypes.CatalogBudget) (apptypes.CatalogInventoryGate, error)
}

// CatalogTargetReservationStore owns proof-free target reserve/release epochs.
type CatalogTargetReservationStore interface {
	ReserveCatalogTarget(context.Context, apptypes.CatalogReservation) (apptypes.CatalogHead, error)
	ReleaseCatalogReservation(context.Context, apptypes.CatalogRelease) (apptypes.CatalogHead, error)
}

// CatalogTargetPlanner atomically selects and reserves one deterministic prefix.
type CatalogTargetPlanner interface {
	PlanAndReserveCatalogTarget(context.Context, apptypes.CatalogTargetPlanRequest) (apptypes.CatalogTargetPlan, error)
}

// CatalogRangeReader returns current or epoch-pinned derived source ranges.
type CatalogRangeReader interface {
	CurrentCatalogRanges(context.Context, apptypes.CatalogBudget) (apptypes.CatalogSnapshot, error)
	CatalogRangesAtEpoch(context.Context, int64, apptypes.CatalogBudget) (apptypes.CatalogSnapshot, error)
}

// CatalogCurrentRebuilder repairs only the non-authoritative derived cache.
type CatalogCurrentRebuilder interface {
	RebuildCatalogCurrentRanges(context.Context, apptypes.CatalogHead, apptypes.CatalogBudget) (apptypes.CatalogRebuildResult, error)
	AuditCatalogLedgerPage(context.Context, apptypes.CatalogAuditCursor, apptypes.CatalogBudget) (apptypes.CatalogAuditPage, error)
}
