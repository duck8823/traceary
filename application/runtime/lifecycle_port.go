package runtime

import (
	"context"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
)

// LifecycleSnapshot is safe history/configuration, not current native authority.
// Clean/PreviousClean describe daemon marker publication only: neither proves
// old process/descendant death, delivery certainty, or safe native-session resume.
type LifecycleSnapshot struct {
	Epoch, PreviousEpoch string
	Clean, PreviousClean bool
	Interrupted          []domain.Interruption
	Shutdowns            []ShutdownResult
	AutoStartOptIn       bool
	UpdateDeferral       types.Optional[domain.UpdateDeferralState]
}

// LifecycleRepository shares the private metadata root budget, never core data.
// Epoch methods are trusted owner operations under the daemon lifetime lease.
// RecordCleanShutdown is called only after actual Runtime ownership/coordinator
// joins; the repository cannot certify OS process death from persisted records.
type LifecycleRepository interface {
	ShutdownJournal
	RecordEpochStart(context.Context, string) (LifecycleSnapshot, error)
	ReadLifecycle(context.Context) (LifecycleSnapshot, error)
	RecordCleanShutdown(context.Context, string) error
	SaveUpdateDeferral(context.Context, string, domain.UpdateDeferralState) error
	ClearUpdateDeferral(context.Context, string) error
	SetAutoStartOptIn(context.Context, bool) error
}

// LifecycleReader exposes history only, without epoch/admission mutation rights.
type LifecycleReader interface {
	ReadLifecycle(context.Context) (LifecycleSnapshot, error)
}
