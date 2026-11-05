package runtime

// InterruptionState is historical uncertainty, never a live owner or resume grant.
type InterruptionState struct {
	Binding         FreshnessBinding
	ProcessUnknown  bool
	PendingControls int
}

// Interruption preserves the original binding without inventing process exit or
// a native conversation identity. The original metadata/receipts remain intact.
type Interruption struct{ state InterruptionState }

// InterruptionFrom validates bounded evidence that actually needs reconciliation.
func InterruptionFrom(s InterruptionState) (Interruption, error) {
	if !s.Binding.valid() || s.PendingControls < 0 || s.PendingControls > MaxControlReceipts || !s.ProcessUnknown && s.PendingControls == 0 {
		return Interruption{}, ErrInvalid
	}
	return Interruption{state: s}, nil
}

// InterruptionOf classifies one prior-epoch execution without inspecting its PID.
func InterruptionOf(snapshot RunSnapshot, pendingControls int) (Interruption, error) {
	if snapshot.State != RunReserved && snapshot.State != RunLive && snapshot.State != RunExited {
		return Interruption{}, ErrInvalid
	}
	return InterruptionFrom(InterruptionState{Binding: FreshnessBinding{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}, ProcessUnknown: snapshot.State != RunExited || snapshot.PublicationUncertain, PendingControls: pendingControls})
}

// Snapshot returns safe independent history only.
func (i Interruption) Snapshot() InterruptionState { return i.state }

// Status never asserts a recorded PID is alive or safe to resume.
func (i Interruption) Status() string {
	if i.state.ProcessUnknown {
		return "orphaned"
	}
	return "historical_unknown"
}
