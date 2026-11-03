package runtime

import (
	"crypto/subtle"
	"math"
)

// Control admission limits reserve lifecycle capacity without forgetting unknown effects.
const (
	MaxControlInputBytes      = 16 * 1024
	MaxControlReceipts        = 1024
	ReservedLifecycleReceipts = 16
	MaxControlCoordinators    = 32
)

// ControlKind names a bounded operation, never an arbitrary signal or command.
type ControlKind string

// Supported controls concern the actual owned terminal, not native approval telemetry.
const (
	ControlInput  ControlKind = "input"
	ControlResize ControlKind = "resize"
	ControlStop   ControlKind = "stop"
)

// ControlOutcome describes kernel delivery evidence, never agent understanding or exit.
type ControlOutcome string

// Pending records become terminal exactly once; unknown effects are never replayed.
const (
	ControlPending   ControlOutcome = "pending"
	ControlDelivered ControlOutcome = "delivered"
	ControlUnknown   ControlOutcome = "unknown"
	ControlRejected  ControlOutcome = "rejected"
)

// ControlIntentState contains safe request metadata, never raw bytes or fingerprints.
// Explicit flags classify operator intent; they are not proof of human identity.
type ControlIntentState struct {
	RequestID                            string
	Kind                                 ControlKind
	ManualTerminal, WorkLossAcknowledged bool
	InputBytes                           int
	ResizeSequence                       uint64
	Rows, Cols                           uint16
}

// ControlIntent is a validated immutable operation shape, not an authorization capability.
type ControlIntent struct{ state ControlIntentState }

// ControlIntentFrom rejects cross-kind fields and automated input before any effect.
func ControlIntentFrom(s ControlIntentState) (ControlIntent, error) {
	if !validControlRequestID(s.RequestID) {
		return ControlIntent{}, ErrInvalid
	}
	switch s.Kind {
	case ControlInput:
		if !s.ManualTerminal || s.WorkLossAcknowledged || s.InputBytes < 1 || s.InputBytes > MaxControlInputBytes || s.ResizeSequence != 0 || s.Rows != 0 || s.Cols != 0 {
			return ControlIntent{}, ErrInvalid
		}
	case ControlResize:
		if s.ManualTerminal || s.WorkLossAcknowledged || s.InputBytes != 0 || s.ResizeSequence == 0 || s.Rows == 0 || s.Cols == 0 {
			return ControlIntent{}, ErrInvalid
		}
	case ControlStop:
		if s.ManualTerminal || !s.WorkLossAcknowledged || s.InputBytes != 0 || s.ResizeSequence != 0 || s.Rows != 0 || s.Cols != 0 {
			return ControlIntent{}, ErrInvalid
		}
	default:
		return ControlIntent{}, ErrInvalid
	}
	return ControlIntent{state: s}, nil
}
func validControlRequestID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// Snapshot returns safe immutable intent metadata.
func (i ControlIntent) Snapshot() ControlIntentState { return i.state }

// ControlSnapshot is a transport-independent metadata receipt without input content.
type ControlSnapshot struct {
	Identity     LaunchIdentity
	Intent       ControlIntentState
	Outcome      ControlOutcome
	WrittenBytes int
}

// ControlReceipt is validated delivery evidence, not permission to replay a request.
type ControlReceipt struct{ state ControlSnapshot }

// ControlReceiptFrom checks byte counts and result semantics on restore and publication.
func ControlReceiptFrom(s ControlSnapshot) (ControlReceipt, error) {
	if !s.Identity.Valid() {
		return ControlReceipt{}, ErrInvalid
	}
	if _, err := ControlIntentFrom(s.Intent); err != nil {
		return ControlReceipt{}, err
	}
	if s.WrittenBytes < 0 || s.WrittenBytes > s.Intent.InputBytes {
		return ControlReceipt{}, ErrInvalid
	}
	switch s.Outcome {
	case ControlPending, ControlRejected:
		if s.WrittenBytes != 0 {
			return ControlReceipt{}, ErrInvalid
		}
	case ControlDelivered:
		if s.Intent.Kind == ControlInput && s.WrittenBytes != s.Intent.InputBytes {
			return ControlReceipt{}, ErrInvalid
		}
	case ControlUnknown:
	default:
		return ControlReceipt{}, ErrInvalid
	}
	return ControlReceipt{state: s}, nil
}

// Snapshot returns only safe receipt metadata, not the live conflict fingerprint.
func (r ControlReceipt) Snapshot() ControlSnapshot { return r.state }

type controlEntry struct {
	snapshot    ControlSnapshot
	fingerprint [32]byte
}

// ControlLedger is a per-owned-run immutable receipt ledger. The application owner
// serializes access. Live HMAC fingerprints are private and must never be persisted.
type ControlLedger struct {
	identity     LaunchIdentity
	entries      map[string]controlEntry
	nonLifecycle int
}

// NewControlLedger binds an empty ledger to one immutable launch identity.
func NewControlLedger(id LaunchIdentity) (*ControlLedger, error) {
	if !id.Valid() {
		return nil, ErrInvalid
	}
	return &ControlLedger{identity: id, entries: make(map[string]controlEntry)}, nil
}

// Inspect checks an existing request before liveness, capacity or filesystem gates.
func (l *ControlLedger) Inspect(intent ControlIntent, fingerprint [32]byte) (ControlSnapshot, bool, error) {
	if _, err := ControlIntentFrom(intent.state); err != nil {
		return ControlSnapshot{}, false, err
	}
	entry, found := l.entries[intent.state.RequestID]
	if !found {
		return ControlSnapshot{}, false, nil
	}
	if entry.snapshot.Intent != intent.state || subtle.ConstantTimeCompare(entry.fingerprint[:], fingerprint[:]) != 1 {
		return ControlSnapshot{}, true, ErrChanged
	}
	return entry.snapshot, true, nil
}

// Reserve returns existing immutable evidence or admits exactly one bounded intent.
func (l *ControlLedger) Reserve(intent ControlIntent, fingerprint [32]byte) (ControlSnapshot, bool, error) {
	existing, found, err := l.Inspect(intent, fingerprint)
	if err != nil || found {
		return existing, found, err
	}
	if len(l.entries) >= MaxControlReceipts || intent.state.Kind != ControlStop && l.nonLifecycle >= MaxControlReceipts-ReservedLifecycleReceipts {
		return ControlSnapshot{}, false, ErrLimit
	}
	snapshot := ControlSnapshot{Identity: l.identity, Intent: intent.state, Outcome: ControlPending}
	l.entries[intent.state.RequestID] = controlEntry{snapshot: snapshot, fingerprint: fingerprint}
	if intent.state.Kind != ControlStop {
		l.nonLifecycle++
	}
	return snapshot, false, nil
}

// Lookup reads a receipt without requiring live child or target witnesses.
func (l *ControlLedger) Lookup(id string) (ControlSnapshot, error) {
	entry, found := l.entries[id]
	if !found {
		return ControlSnapshot{}, ErrNotFound
	}
	return entry.snapshot, nil
}

// Complete validates a terminal result and never rewrites earlier terminal evidence.
func (l *ControlLedger) Complete(id string, outcome ControlOutcome, written int) (ControlSnapshot, error) {
	entry, found := l.entries[id]
	if !found {
		return ControlSnapshot{}, ErrNotFound
	}
	candidate := entry.snapshot
	candidate.Outcome = outcome
	candidate.WrittenBytes = written
	if outcome == ControlPending {
		return ControlSnapshot{}, ErrInvalid
	}
	if _, err := ControlReceiptFrom(candidate); err != nil {
		return ControlSnapshot{}, err
	}
	if entry.snapshot.Outcome != ControlPending {
		if entry.snapshot == candidate {
			return entry.snapshot, nil
		}
		return ControlSnapshot{}, ErrChanged
	}
	entry.snapshot = candidate
	l.entries[id] = entry
	return candidate, nil
}

// AbandonUncommitted is only for a journal's definite precommit failure. The owner
// must never call it on an ambiguous commit; unknown and completed effects cannot be removed.
func (l *ControlLedger) AbandonUncommitted(id string) error {
	entry, found := l.entries[id]
	if !found {
		return ErrNotFound
	}
	if entry.snapshot.Outcome != ControlPending {
		return ErrChanged
	}
	delete(l.entries, id)
	if entry.snapshot.Intent.Kind != ControlStop {
		l.nonLifecycle--
	}
	return nil
}

// ValidateResizeSequence admits only the next owner-applied resize without wrapping.
func ValidateResizeSequence(current, next uint64) error {
	if current == math.MaxUint64 {
		return ErrLimit
	}
	if next != current+1 {
		return ErrChanged
	}
	return nil
}
