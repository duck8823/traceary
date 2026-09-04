package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// PageMetadataInspector reads O(1) SQLite page accounting from a store
// path. It must not walk dbstat, apply migrations, or take the coordinated
// store lease.
type PageMetadataInspector interface {
	InspectPageMetadata(ctx context.Context, dbPath string) (apptypes.StorePageMetadata, error)
}
