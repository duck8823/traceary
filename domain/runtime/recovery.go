package runtime

// RecoveryBindingState contains native identity metadata, never input or argv.
// Validation does not attest a native source or grant process-control authority.
type RecoveryBindingState struct {
	Identity               LaunchIdentity
	Target                 Target
	Profile                HostProfile
	PID                    int
	NativeSessionID        string
	NativeHistoryPath      string
	NativeNamespaceWitness string
	Model                  string
	Provider               string
}

// RecoveryBinding is an immutable selection supplied by a qualified native adapter.
// A client-provided observation must never be promoted into this trusted role.
type RecoveryBinding struct{ state RecoveryBindingState }

// RecoveryBindingOf validates representation; it performs no native or OS I/O.
func RecoveryBindingOf(s RecoveryBindingState) (RecoveryBinding, error) {
	if _, err := LaunchDescriptorOf(s.Identity, s.Target, s.Profile); err != nil {
		return RecoveryBinding{}, ErrInvalid
	}
	if s.PID <= 0 || !observationCorrelation(s.NativeSessionID, false) || !validPath(s.NativeHistoryPath) || !validMetadataDigest(s.NativeNamespaceWitness) || !printableLaunchValue(s.Model, 256) || !printableLaunchValue(s.Provider, 256) {
		return RecoveryBinding{}, ErrInvalid
	}
	return RecoveryBinding{state: s}, nil
}

// Snapshot returns a value copy, not evidence of liveness or input readiness.
func (b RecoveryBinding) Snapshot() RecoveryBindingState { return b.state }

// ConversationKey excludes profile/model/version/target aliases. It coordinates
// Traceary's managed operations only, not external native CLI processes.
func (b RecoveryBinding) ConversationKey() string {
	if _, err := RecoveryBindingOf(b.state); err != nil {
		return ""
	}
	return identity("n_", b.state.Profile.Snapshot().Host, b.state.NativeNamespaceWitness, b.state.NativeSessionID)
}

// RecoveryRecordState is private persisted bookkeeping, never a public write DTO.
// ExitObserved may be produced only by Runtime's actual owned Wait reconciliation.
type RecoveryRecordState struct {
	Binding      RecoveryBinding
	ExitObserved bool
	ExitCode     int
}

// RecoveryRecord owns the one-way observation of its exact managed AI's exit.
// It does not certify that external actors or descendants have stopped.
type RecoveryRecord struct{ state RecoveryRecordState }

// NewRecoveryRecord begins without an observed exit, regardless of its saved PID.
func NewRecoveryRecord(binding RecoveryBinding) (*RecoveryRecord, error) {
	return RecoveryRecordFrom(RecoveryRecordState{Binding: binding})
}

// RecoveryRecordFrom reconstructs bookkeeping, not native/OS proof. Infrastructure
// must validate its original execution row under the same private metadata lock.
func RecoveryRecordFrom(s RecoveryRecordState) (*RecoveryRecord, error) {
	if _, err := RecoveryBindingOf(s.Binding.state); err != nil || !s.ExitObserved && s.ExitCode != 0 || s.ExitObserved && (s.ExitCode < -1 || s.ExitCode > 255) {
		return nil, ErrInvalid
	}
	return &RecoveryRecord{state: s}, nil
}

// Snapshot returns a value copy. A persisted receipt cannot control any saved PID.
func (r *RecoveryRecord) Snapshot() RecoveryRecordState {
	if r == nil {
		return RecoveryRecordState{}
	}
	return r.state
}

// ObserveOwnedExit must be called only from actual owned-Wait reconciliation,
// never from PID disappearance, a lifecycle label or an untrusted request flag.
func (r *RecoveryRecord) ObserveOwnedExit(id LaunchIdentity, code int) error {
	if r == nil || code < -1 || code > 255 {
		return ErrInvalid
	}
	if id != r.state.Binding.state.Identity || r.state.ExitObserved && r.state.ExitCode != code {
		return ErrChanged
	}
	r.state.ExitObserved = true
	r.state.ExitCode = code
	return nil
}

// ObserveQualifiedOwnedExit seals the actual final model of the SAME native
// conversation/process at owned exit. It cannot retarget source, provider,
// history, native identity or PID, and a sealed receipt never changes again.
// The application must qualify the native binding and actual Wait separately.
func (r *RecoveryRecord) ObserveQualifiedOwnedExit(id LaunchIdentity, code int, binding RecoveryBinding) error {
	if r == nil {
		return ErrInvalid
	}
	if _, err := RecoveryBindingOf(binding.state); err != nil {
		return err
	}
	expected := r.state.Binding.state
	expected.Model = binding.state.Model
	if expected != binding.state || r.state.ExitObserved && r.state.Binding != binding {
		return ErrChanged
	}
	if err := r.ObserveOwnedExit(id, code); err != nil {
		return err
	}
	r.state.Binding = binding
	return nil
}
