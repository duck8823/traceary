package runtime

import (
	"context"
	"errors"
	"slices"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// ErrUpdateUnknown forbids automatic replay after possibly persisted update
// metadata. It is not evidence that software was installed or a daemon exited.
var ErrUpdateUnknown = errors.New("daemon update metadata outcome uncertain; inspect status without replay")

// UpdateAttemptRequest binds safe target metadata to the current daemon epoch.
type UpdateAttemptRequest struct {
	Epoch  string
	Target domain.UpdateTarget
}

// UpdateClient is separate from legacy transport/shutdown implementers. None of
// these methods starts a daemon, installs software or changes host settings.
type UpdateClient interface {
	UpdateStatus(context.Context, string, string) (UpdateStatus, error)
	AttemptUpdate(context.Context, string, UpdateAttemptRequest) (UpdateAttemptResult, error)
	HandoffUpdate(context.Context, string, UpdateHandoffRequest) (ShutdownResult, error)
}

// Valid checks bounded metadata syntax, not native authority or installation.
func (r UpdateAttemptRequest) Valid() bool {
	return (domain.LaunchIdentity{Epoch: r.Epoch, Run: r.Epoch, Generation: r.Epoch}).Valid() && r.Target.Valid()
}

var updateBlockerOrder = []string{"active_runs", "pending_launches", "pending_controls", "pending_reconciliations", "lifecycle_busy", "publication_uncertain", "admission_closed", "revision_exhausted"}

// Valid rejects contradictory, duplicate, reordered or unknown blocker evidence.
func (s UpdateStatus) Valid() bool {
	if !(domain.LaunchIdentity{Epoch: s.Epoch, Run: s.Epoch, Generation: s.Epoch}).Valid() || s.Blockers == nil || len(s.Blockers) > len(updateBlockerOrder) || s.Quiescent != (len(s.Blockers) == 0) {
		return false
	}
	last := -1
	for _, blocker := range s.Blockers {
		index := slices.Index(updateBlockerOrder, blocker)
		if index <= last {
			return false
		}
		last = index
	}
	state, found := s.Deferral.Value()
	if !found {
		return !s.Overdue24h
	}
	_, err := domain.UpdateDeferralFrom(state)
	return err == nil && state.Started.Year() >= 0 && state.Started.Year() <= 9999
}

// Valid allows an omitted preview on a known failed attempt; successful callers
// additionally require preview iff quiescent. An old/foreign/nonempty preview
// never becomes an empty handoff grant.
func (r UpdateAttemptResult) Valid() bool {
	if !r.Target.Valid() || !r.Notice || !r.Status.Valid() {
		return false
	}
	binding, found := r.Preview.Value()
	if !found {
		return true
	}
	_, err := domain.ShutdownBindingFrom(binding)
	return err == nil && r.Status.Quiescent && binding.Epoch == r.Status.Epoch && emptyUpdateBinding(binding)
}

// Valid checks complete explicit empty-handoff input, never human identity.
func (r UpdateHandoffRequest) Valid() bool {
	_, err := domain.ShutdownBindingFrom(r.Binding)
	return err == nil && r.Target.Valid() && r.DaemonExitAcknowledged && emptyUpdateBinding(r.Binding)
}
