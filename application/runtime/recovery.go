package runtime

import (
	"context"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// RecoveryBindingSource belongs to the actual owned native child adapter, not
// hook/client observations. At explicit resume confirmation or after Wait, it returns metadata only if its exact
// supported native CLI/source, conversation, target and model/provider verify.
// Missing/delayed/unsupported evidence must return an error. Implementations
// must honor the bounded context and never resume, adopt, signal or replay input.
type RecoveryBindingSource interface {
	RecoveryBinding(context.Context) (domain.RecoveryBinding, error)
}

// RecoveryRecorder persists application-produced receipts. No public protocol
// may expose it as a client-supplied exit/verified flag or raw binding write.
type RecoveryRecorder interface {
	SaveRecovery(context.Context, domain.RecoveryRecordState) error
}

// persistOwnedRecovery is reached only after actual Wait AND durable execution
// exit. Optional native evidence never changes a normal launch/exit outcome.
// Publication uncertainty is not liveness: a separately observed exact owned
// exit can still qualify; missing exit proof can never be replaced by a PID.
func (s *Runtime) persistOwnedRecovery(child OwnedChild, snapshot domain.RunSnapshot, expected domain.RecoveryBinding, published bool) {
	source, ok := child.(RecoveryBindingSource)
	if !ok {
		return
	}
	journal, ok := s.journal.(RecoveryRecorder)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	binding, err := source.RecoveryBinding(ctx)
	if err != nil || ctx.Err() != nil {
		return
	}
	state := binding.Snapshot()
	if state.Identity != snapshot.Identity || state.Target != snapshot.Target || state.Profile != snapshot.Profile || state.PID != snapshot.PID || snapshot.State != domain.RunExited {
		return
	}
	if expected != (domain.RecoveryBinding{}) {
		wanted := expected.Snapshot()
		wanted.Identity = snapshot.Identity
		wanted.PID = snapshot.PID
		if published {
			// A published interactive run may explicitly change model after its
			// original resume was confirmed. Keep every identity/provider field
			// exact and record the actual final model for the next recovery.
			// An unknown/unpublished startup still requires the original model.
			wanted.Model = state.Model
		}
		verified, err := domain.RecoveryBindingOf(wanted)
		if err != nil || verified != binding {
			return
		}
	}
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		return
	}
	if err := record.ObserveOwnedExit(snapshot.Identity, snapshot.ExitCode); err != nil {
		return
	}
	// Failure means recovery remains unavailable. Never rewrite process history,
	// automatically retry this write, or mark a normal exit as a native failure.
	_ = journal.SaveRecovery(ctx, record.Snapshot())
}
