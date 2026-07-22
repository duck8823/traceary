package queryservice

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// WorkspaceIdentityQueryService provides body-free attribution diagnostics.
type WorkspaceIdentityQueryService interface {
	WorkspaceIdentityReport(ctx context.Context, conflictSampleLimit int) (apptypes.WorkspaceIdentityReport, error)
}
