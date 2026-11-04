package runtime

// ResumePhase describes a single managed recovery request, not native liveness.
type ResumePhase string

// Phases never imply permission to execute or replay a stored request.
const (
	ResumeReserved          ResumePhase = "reserved"
	ResumeStarted           ResumePhase = "started"
	ResumeConfirmed         ResumePhase = "confirmed"
	ResumeUnknown           ResumePhase = "unknown"
	ResumeFailedBeforeStart ResumePhase = "failed_before_start"
)

// ResumeAttemptState intentionally has no input, prompt, approval, tool or argv
// field. Reloaded reserved/unknown outcomes are inspect-only, not replay work.
type ResumeAttemptState struct {
	RequestID           string
	Binding             RecoveryBinding
	PredecessorExitCode int
	Next                LaunchIdentity
	Phase               ResumePhase
	Confirmed           RecoveryBinding
}

// ResumeAttempt owns monotonic outcome transitions. Runtime/store additionally
// own request deduplication, conversation serialization and actual child handles.
type ResumeAttempt struct{ state ResumeAttemptState }

// NewResumeAttempt requires positive recorded owned-exit bookkeeping. The caller
// must still revalidate its native source/target and hold the managed reservation.
func NewResumeAttempt(requestID string, record *RecoveryRecord, next LaunchIdentity) (*ResumeAttempt, error) {
	if record == nil {
		return nil, ErrInvalid
	}
	if !record.state.ExitObserved {
		return nil, ErrChanged
	}
	return ResumeAttemptFrom(ResumeAttemptState{
		RequestID: requestID, Binding: record.state.Binding, PredecessorExitCode: record.state.ExitCode,
		Next: next, Phase: ResumeReserved,
	})
}

// ResumeAttemptFrom reconstructs metadata only. It never continues a stored
// reservation or reconstructs an actual in-memory process owner.
func ResumeAttemptFrom(s ResumeAttemptState) (*ResumeAttempt, error) {
	if !validControlRequestID(s.RequestID) || !s.Next.Valid() || s.PredecessorExitCode < -1 || s.PredecessorExitCode > 255 {
		return nil, ErrInvalid
	}
	if _, err := RecoveryBindingOf(s.Binding.state); err != nil || s.Next.Run == s.Binding.state.Identity.Run || s.Next.Generation == s.Binding.state.Identity.Generation {
		return nil, ErrInvalid
	}
	switch s.Phase {
	case ResumeReserved, ResumeStarted, ResumeUnknown, ResumeFailedBeforeStart:
		if s.Confirmed != (RecoveryBinding{}) {
			return nil, ErrInvalid
		}
	case ResumeConfirmed:
		if !matchingResumeBinding(s, s.Confirmed) {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	return &ResumeAttempt{state: s}, nil
}

// Snapshot is defensive metadata, not permission to spawn, adopt or send input.
func (a *ResumeAttempt) Snapshot() ResumeAttemptState {
	if a == nil {
		return ResumeAttemptState{}
	}
	return a.state
}

// MarkStarted records transfer of an actual child. It cannot restart uncertainty.
func (a *ResumeAttempt) MarkStarted() error {
	if a == nil {
		return ErrInvalid
	}
	switch a.state.Phase {
	case ResumeReserved, ResumeStarted:
		a.state.Phase = ResumeStarted
		return nil
	default:
		return ErrChanged
	}
}

// FailBeforeStart is only for the still-owning operation with a proven no-child
// outcome. A loaded reservation is not such proof and must never call this path.
func (a *ResumeAttempt) FailBeforeStart() error {
	if a == nil {
		return ErrInvalid
	}
	switch a.state.Phase {
	case ResumeReserved, ResumeFailedBeforeStart:
		a.state.Phase = ResumeFailedBeforeStart
		return nil
	default:
		return ErrChanged
	}
}

// MarkUnknown retains a possibly committed start; it never rearms the attempt.
func (a *ResumeAttempt) MarkUnknown() error {
	if a == nil {
		return ErrInvalid
	}
	switch a.state.Phase {
	case ResumeReserved, ResumeStarted, ResumeUnknown:
		a.state.Phase = ResumeUnknown
		return nil
	default:
		return ErrChanged
	}
}

// Confirm reconciles metadata from that SAME actual successor child, including
// after uncertainty. It is not an operation to create another child. Runtime must
// persist this result before clearing its input/publication confirmation gate.
func (a *ResumeAttempt) Confirm(successor RecoveryBinding) error {
	if a == nil {
		return ErrInvalid
	}
	if !matchingResumeBinding(a.state, successor) {
		return ErrChanged
	}
	switch a.state.Phase {
	case ResumeStarted, ResumeUnknown:
		a.state.Confirmed = successor
		a.state.Phase = ResumeConfirmed
		return nil
	case ResumeConfirmed:
		if a.state.Confirmed == successor {
			return nil
		}
		return ErrChanged
	default:
		return ErrChanged
	}
}

func matchingResumeBinding(attempt ResumeAttemptState, successor RecoveryBinding) bool {
	if _, err := RecoveryBindingOf(successor.state); err != nil || successor.state.Identity != attempt.Next {
		return false
	}
	state := successor.state
	state.Identity = attempt.Binding.state.Identity
	state.PID = attempt.Binding.state.PID
	return state == attempt.Binding.state
}
