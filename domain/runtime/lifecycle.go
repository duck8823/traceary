package runtime

import (
	"encoding/hex"
	"math"
	"strings"
	"time"
)

// ShutdownBindingState identifies the entire preview, not one roster page.
// Public identifiers/digests correlate a plan; none attest human authorization.
type ShutdownBindingState struct {
	Epoch, PreviewID                                                    string
	Revision                                                            uint64
	RosterDigest                                                        string
	TotalRuns, PendingLaunches, PendingControls, PendingReconciliations int
}

// ShutdownBinding is an immutable, epoch-scoped work-loss confirmation boundary.
type ShutdownBinding struct{ state ShutdownBindingState }

// ShutdownBindingFrom validates a complete bounded preview binding.
func ShutdownBindingFrom(s ShutdownBindingState) (ShutdownBinding, error) {
	if !validLaunchID(s.Epoch) || !validLaunchID(s.PreviewID) || !canonicalHex(s.RosterDigest, 64) ||
		s.TotalRuns < 0 || s.TotalRuns > MaxActiveRuns || s.PendingLaunches < 0 || s.PendingLaunches > MaxActiveRuns ||
		s.PendingControls < 0 || s.PendingControls > MaxControlCoordinators || s.PendingReconciliations < 0 || s.PendingReconciliations > MaxActiveRuns {
		return ShutdownBinding{}, ErrInvalid
	}
	return ShutdownBinding{state: s}, nil
}

// Snapshot returns independent safe plan metadata.
func (b ShutdownBinding) Snapshot() ShutdownBindingState { return b.state }

// Confirm requires a quiescent, unchanged full roster and explicit work-loss acknowledgment.
func (b ShutdownBinding) Confirm(current ShutdownBindingState, workLossAcknowledged bool) error {
	if !workLossAcknowledged {
		return ErrInvalid
	}
	if _, err := ShutdownBindingFrom(b.state); err != nil {
		return err
	}
	if _, err := ShutdownBindingFrom(current); err != nil {
		return err
	}
	if b.state != current || current.PendingLaunches != 0 || current.PendingControls != 0 || current.PendingReconciliations != 0 {
		return ErrChanged
	}
	return nil
}
func canonicalHex(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// FreshnessBinding is a complete managed selection, never a saved PID.
type FreshnessBinding struct {
	Identity LaunchIdentity
	Worktree WorktreeID
	Profile  ProfileID
}

func (b FreshnessBinding) valid() bool {
	if !b.Identity.Valid() || !strings.HasPrefix(string(b.Worktree), "w_") || !canonicalHex(strings.TrimPrefix(string(b.Worktree), "w_"), 64) {
		return false
	}
	_, err := ProfileIDFrom(string(b.Profile))
	return err == nil
}

// FreshnessReason records loss of causal interaction knowledge, not process state.
type FreshnessReason string

// Restart and process exit permanently end this binding's reconciliation lifetime.
const (
	FreshnessSleepWake        FreshnessReason = "sleep_wake"
	FreshnessCommunicationGap FreshnessReason = "communication_gap"
	FreshnessRestart          FreshnessReason = "restart"
	FreshnessProcessExit      FreshnessReason = "process_exit"
)

func (r FreshnessReason) valid() bool {
	switch r {
	case FreshnessSleepWake, FreshnessCommunicationGap, FreshnessRestart, FreshnessProcessExit:
		return true
	default:
		return false
	}
}
func (r FreshnessReason) terminal() bool { return r == FreshnessRestart || r == FreshnessProcessExit }

// FreshnessState is safe reducer state, separate from native interaction content.
type FreshnessState struct {
	Binding   FreshnessBinding
	Revision  uint64
	Known     bool
	Reason    FreshnessReason
	Exhausted bool
}

// Freshness owns monotonic invalidation and prevents old-epoch resurrection.
// It confers no terminal input, approval, or resume authority.
type Freshness struct{ state FreshnessState }

// NewFreshness starts unknown; transport readiness cannot create interaction knowledge.
func NewFreshness(binding FreshnessBinding) (*Freshness, error) {
	return FreshnessFrom(FreshnessState{Binding: binding, Revision: 1})
}

// FreshnessFrom restores validated reducer state, never a live native owner.
func FreshnessFrom(s FreshnessState) (*Freshness, error) {
	if !s.Binding.valid() || s.Revision == 0 || s.Reason != "" && !s.Reason.valid() ||
		s.Known && (s.Exhausted || s.Reason.terminal()) || s.Exhausted && s.Revision != math.MaxUint64 {
		return nil, ErrInvalid
	}
	return &Freshness{state: s}, nil
}

// Snapshot returns state without modifying freshness or consuming notifications.
func (f *Freshness) Snapshot() FreshnessState { return f.state }

// Invalidate rejects overflow without ever leaving previous evidence current.
func (f *Freshness) Invalidate(reason FreshnessReason) error {
	if !reason.valid() {
		return ErrInvalid
	}
	f.state.Known = false
	// A nonterminal invalidation must never erase a permanent terminal reason.
	if !f.state.Reason.terminal() {
		f.state.Reason = reason
	}
	if f.state.Revision == math.MaxUint64 {
		f.state.Exhausted = true
		return ErrLimit
	}
	f.state.Revision++
	return nil
}

// CausalFreshnessEvidence is for trusted native-binding ingestion ONLY. The
// adapter must validate native source/session/event causality before calling
// Reconcile, and stamp Revision at intake, not at delayed dequeue. Nonempty
// strings alone are not proof. This is not a management/heartbeat API.
type CausalFreshnessEvidence struct {
	Binding                    FreshnessBinding
	Revision                   uint64
	NativeSession, NativeEvent string
}

// Reconcile accepts only the current challenge for the entire managed binding.
func (f *Freshness) Reconcile(e CausalFreshnessEvidence) error {
	if f.state.Exhausted || f.state.Reason.terminal() || e.Binding != f.state.Binding || e.Revision != f.state.Revision {
		return ErrChanged
	}
	if !printableLaunchValue(e.NativeSession, 256) || !printableLaunchValue(e.NativeEvent, 256) {
		return ErrInvalid
	}
	f.state.Known = true
	return nil
}

// UpdateTarget is metadata only: never an executable, URL, or updater command.
type UpdateTarget struct {
	Version  string
	Protocol int
}

func (t UpdateTarget) valid() bool {
	if len(t.Version) == 0 || len(t.Version) > 128 || t.Protocol < 1 || t.Protocol > math.MaxUint16 {
		return false
	}
	for _, r := range t.Version {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' || r == '+'
		if !valid {
			return false
		}
	}
	return true
}

// UpdateDeferralState preserves a continuous deferral across target changes.
type UpdateDeferralState struct {
	Target      UpdateTarget
	Started     time.Time
	Notified24h bool
}

// UpdateDeferral owns notices only; it never authorizes an updater or stops runs.
type UpdateDeferral struct{ state UpdateDeferralState }

// NewUpdateDeferral begins one continuous deferral, with no automatic effects.
func NewUpdateDeferral(target UpdateTarget, now time.Time) (*UpdateDeferral, error) {
	return UpdateDeferralFrom(UpdateDeferralState{Target: target, Started: now})
}

// UpdateDeferralFrom restores bounded safe metadata without resetting the clock.
func UpdateDeferralFrom(s UpdateDeferralState) (*UpdateDeferral, error) {
	if !s.Target.valid() || s.Started.IsZero() {
		return nil, ErrInvalid
	}
	return &UpdateDeferral{state: s}, nil
}

// Snapshot is read-only, including after a reminder has been published.
func (d *UpdateDeferral) Snapshot() UpdateDeferralState { return d.state }

// Retarget preserves the continuous deferral start and prior notice state.
func (d *UpdateDeferral) Retarget(target UpdateTarget) error {
	if !target.valid() {
		return ErrInvalid
	}
	d.state.Target = target
	return nil
}

// Overdue24h is non-consuming; a lost reply cannot hide an overdue deferral.
func (d *UpdateDeferral) Overdue24h(now time.Time) bool {
	return !now.Before(d.state.Started.Add(24 * time.Hour))
}

// UpdateNoticeTrigger distinguishes an explicit attempt from notice publication.
type UpdateNoticeTrigger string

// Notice triggers never imply permission to execute an update.
const (
	UpdateAttempt UpdateNoticeTrigger = "attempt"
	UpdateStatus  UpdateNoticeTrigger = "notice_publication"
)

// Notice is a publication transition, NOT a read-only status call. The application
// persists the resulting state with its notice; every explicit attempt stays visible.
func (d *UpdateDeferral) Notice(now time.Time, trigger UpdateNoticeTrigger) bool {
	switch trigger {
	case UpdateAttempt:
		return true
	case UpdateStatus:
		if d.state.Notified24h || !d.Overdue24h(now) {
			return false
		}
		d.state.Notified24h = true
		return true
	default:
		return false
	}
}

// ShutdownOutcome separates terminal closure from proof of native process exit.
type ShutdownOutcome string

// A draining receipt asserts terminal closure only, never process death.
const (
	ShutdownPending  ShutdownOutcome = "pending"
	ShutdownDraining ShutdownOutcome = "draining"
	ShutdownUnknown  ShutdownOutcome = "unknown"
	ShutdownRejected ShutdownOutcome = "rejected"
)
