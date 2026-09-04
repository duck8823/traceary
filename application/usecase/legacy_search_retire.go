//nolint:wrapcheck // Adapter errors retain their typed identity at the CLI boundary.
package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// LegacySearchRetireStore drops the migration-032 search index family.
type LegacySearchRetireStore interface {
	RetireLegacySearchFamily(context.Context) (apptypes.LegacySearchRetireReport, error)
}

// LegacySearchRetireUsecase is the single-command retirement workflow.
type LegacySearchRetireUsecase struct {
	store LegacySearchRetireStore
}

// NewLegacySearchRetireUsecase composes the opt-in family removal command.
func NewLegacySearchRetireUsecase(store LegacySearchRetireStore) *LegacySearchRetireUsecase {
	return &LegacySearchRetireUsecase{store: store}
}

// Retire drops the legacy search family in one transaction, or reports that
// it is already gone.
func (u *LegacySearchRetireUsecase) Retire(ctx context.Context) (apptypes.LegacySearchRetireReport, error) {
	return u.store.RetireLegacySearchFamily(ctx)
}
