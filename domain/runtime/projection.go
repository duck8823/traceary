package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// Projection limits never evict event identities and thereby make replay new.
const (
	MaxProjectionEvents = 256
	InteractionTTL      = 60 * time.Second
)

// ProjectionSelection is correlation, not a native binding or permission.
type ProjectionSelection struct {
	Identity LaunchIdentity
	Worktree WorktreeID
	Profile  ProfileID
}

// Valid checks correlation syntax only, never native ownership or permission.
func (b ProjectionSelection) Valid() bool {
	w := string(b.Worktree)
	if !b.Identity.Valid() || len(w) != 66 || !strings.HasPrefix(w, "w_") || w != strings.ToLower(w) {
		return false
	}
	if _, err := hex.DecodeString(w[2:]); err != nil {
		return false
	}
	_, err := ProfileIDFrom(string(b.Profile))
	return err == nil
}

// ProjectionClock is supplied by a local epoch-monotonic clock, never decoded
// from telemetry. Wall is display-only. Received must be the ORIGINAL intake.
type ProjectionClock struct {
	Elapsed time.Duration
	Wall    time.Time
}

// ProjectionKind describes interaction telemetry, never readiness or authority.
type ProjectionKind string

// Interaction classifications carry no process, approval or safe-input authority.
const (
	ProjectionUnknown  ProjectionKind = "unknown"
	ProjectionWorking  ProjectionKind = "working"
	ProjectionWaiting  ProjectionKind = "waiting"
	ProjectionStop     ProjectionKind = "stop"
	ProjectionApproval ProjectionKind = "approval"
)

// ProjectionReason explains unknown knowledge without implying process exit.
type ProjectionReason string

// Unknown reasons describe missing knowledge, not native actions or decisions.
const (
	ProjectionGap             ProjectionReason = "gap"
	ProjectionSleepWake       ProjectionReason = "sleep_wake"
	ProjectionRestart         ProjectionReason = "restart"
	ProjectionExpired         ProjectionReason = "expired"
	ProjectionClockReversal   ProjectionReason = "clock_reversal"
	ProjectionConflict        ProjectionReason = "conflict"
	ProjectionCausality       ProjectionReason = "causality_unknown"
	ProjectionCapacity        ProjectionReason = "capacity"
	ProjectionProcessInactive ProjectionReason = "process_inactive"
)

// ProjectionEventState is bounded metadata only; no native payload or decision.
type ProjectionEventState struct {
	Selection                                           ProjectionSelection
	Source, EventID, NativeSessionID, TurnID, RequestID string
	SourceVersion                                       string
	Boundary                                            ObservationKind
	Kind                                                ProjectionKind
	Received                                            ProjectionClock
	HostTime                                            time.Time
	HasHostTime                                         bool
}

// ProjectionEvent is an immutable syntactically checked observation, NOT proof.
type ProjectionEvent struct {
	state            ProjectionEventState
	key, fingerprint [32]byte
	valid            bool
}

// ProjectionEventOf validates syntax only. In particular, claimed IDs and host
// time do not establish source provenance, native order, continuity or age.
func ProjectionEventOf(s ProjectionEventState) (ProjectionEvent, error) {
	if !s.Selection.Valid() || s.Received.Elapsed < 0 || s.Received.Wall.IsZero() || s.Received.Wall.Year() < 1 || s.Received.Wall.Year() > 9999 || !printableLaunchValue(s.SourceVersion, 256) {
		return ProjectionEvent{}, ErrInvalid
	}
	switch s.Source {
	case "codex", "claude", "antigravity", "grok":
	default:
		return ProjectionEvent{}, ErrInvalid
	}
	if !observationCorrelation(s.EventID, false) || !observationCorrelation(s.NativeSessionID, true) || !observationCorrelation(s.TurnID, true) || !observationCorrelation(s.RequestID, true) {
		return ProjectionEvent{}, ErrInvalid
	}
	switch s.Kind {
	case ProjectionUnknown, ProjectionWorking, ProjectionWaiting, ProjectionStop, ProjectionApproval:
	default:
		return ProjectionEvent{}, ErrInvalid
	}
	switch s.Boundary {
	case "", ObservationPromptBoundary, ObservationToolBoundary, ObservationTurnBoundary, ObservationSessionBoundary:
	default:
		return ProjectionEvent{}, ErrInvalid
	}
	if s.HasHostTime == s.HostTime.IsZero() {
		return ProjectionEvent{}, ErrInvalid
	}
	s.HostTime = s.HostTime.Round(0).UTC()
	s.Received.Wall = s.Received.Wall.Round(0).UTC()
	content := s
	content.Received = ProjectionClock{}
	encoded, err := json.Marshal(content)
	if err != nil {
		return ProjectionEvent{}, ErrInvalid
	}
	return ProjectionEvent{state: s, key: sha256.Sum256([]byte(s.Source + "\x00" + s.EventID)), fingerprint: sha256.Sum256(encoded), valid: true}, nil
}

// Snapshot returns a value copy with original receipt/display metadata.
func (e ProjectionEvent) Snapshot() ProjectionEventState { return e.state }

// NativeProjectionEvidence is a TRUSTED ADAPTER-ONLY port, not a wire DTO.
// The adapter must verify live native provenance, complete launch binding,
// native sequence/predecessor and original local continuity stamp before BOTH
// commit and current reads. Sequence is NEVER a receipt counter. Syntax/IDs or
// this struct alone prove none of those things. No hook conversion grants it.
type NativeProjectionEvidence struct {
	Event              ProjectionEvent
	Revision, Sequence uint64
	PreviousEventID    string
}

// ProjectionAttention identifies one accepted transition, not a notification,
// approval, safe-input signal or process-control handle. Reads do not consume it.
type ProjectionAttention struct {
	Source, EventID string
	Revision        uint64
}

// ProjectionSnapshot keeps current, historical, process and health axes separate.
type ProjectionSnapshot struct {
	// RefreshAfter is read-driven interaction expiry, not a promised channel
	// wake. A subscriber must schedule its own reread no later than this delay.
	RefreshAfter                                              time.Duration
	Selection                                                 ProjectionSelection
	Version, ContinuityRevision                               uint64
	Process                                                   RunState
	Interaction                                               ProjectionKind
	Reason                                                    ProjectionReason
	CaptureHealth                                             CaptureHealth
	ConnectionHealth                                          ConnectionHealth
	LastEvent, LastWaiting                                    ProjectionEventState
	HasLastEvent, HasLastWaiting, LastWaitingStale, Exhausted bool
	Attention                                                 ProjectionAttention
}

// CaptureHealth concerns original capture, not native interaction.
type CaptureHealth string

// Capture classifications are independent of interaction and process state.
const (
	CaptureUnknown  CaptureHealth = "unknown"
	CaptureHealthy  CaptureHealth = "healthy"
	CaptureDegraded CaptureHealth = "degraded"
)

// ConnectionHealth concerns transport, not process liveness or capture success.
type ConnectionHealth string

// Connection classifications describe transport, not interaction or capture.
const (
	ConnectionUnknown      ConnectionHealth = "unknown"
	ConnectionConnected    ConnectionHealth = "connected"
	ConnectionDisconnected ConnectionHealth = "disconnected"
)

type projectionReceipt struct {
	fingerprint [32]byte
	native      bool
	sequence    uint64
	previous    string
}

// ObservationProjection owns causal/expiry invariants. Caller serializes access;
// it has no goroutines, persistence, I/O, native probes or process operations.
type ObservationProjection struct {
	state                           ProjectionSnapshot
	seen                            map[[32]byte]projectionReceipt
	lastClock, acceptedAt           time.Duration
	clockSeen                       bool
	sequence                        uint64
	anchorID, nativeSession, source string
}

// NewObservationProjection creates a selection-scoped unknown projection.
func NewObservationProjection(b ProjectionSelection, process RunState) (*ObservationProjection, error) {
	if !b.Valid() || !projectionProcess(process) {
		return nil, ErrInvalid
	}
	return &ObservationProjection{state: ProjectionSnapshot{Selection: b, Version: 1, ContinuityRevision: 1, Process: process, Interaction: ProjectionUnknown, CaptureHealth: CaptureUnknown, ConnectionHealth: ConnectionUnknown}, seen: make(map[[32]byte]projectionReceipt)}, nil
}
func projectionProcess(s RunState) bool { return s == RunReserved || s == RunLive || s == RunExited }
func (p *ObservationProjection) changed() {
	if p.state.Version == math.MaxUint64 {
		p.state.Exhausted = true
		p.unknown(ProjectionCapacity)
		return
	}
	p.state.Version++
}
func (p *ObservationProjection) unknown(reason ProjectionReason) {
	p.state.Interaction = ProjectionUnknown
	p.state.Reason = reason
	p.state.LastWaitingStale = p.state.HasLastWaiting
}
func (p *ObservationProjection) advance(now ProjectionClock) bool {
	if now.Elapsed < 0 || now.Wall.IsZero() || p.clockSeen && now.Elapsed < p.lastClock {
		p.Invalidate(ProjectionClockReversal)
		return false
	}
	p.clockSeen = true
	p.lastClock = now.Elapsed
	if p.state.Interaction != ProjectionUnknown && now.Elapsed-p.acceptedAt >= InteractionTTL {
		p.unknown(ProjectionExpired)
		p.changed()
	}
	return true
}

// Snapshot expires knowledge by monotonic elapsed age, never by wall clock.
func (p *ObservationProjection) Snapshot(now ProjectionClock) ProjectionSnapshot {
	p.advance(now)
	snapshot := p.state
	if snapshot.Interaction != ProjectionUnknown {
		snapshot.RefreshAfter = InteractionTTL - (now.Elapsed - p.acceptedAt)
	}
	return snapshot
}

// Invalidate revokes old intake revisions without deleting dedup or history.
// A native continuity gate remains the authority for sleep/wake detection.
func (p *ObservationProjection) Invalidate(reason ProjectionReason) {
	switch reason {
	case ProjectionGap, ProjectionSleepWake, ProjectionRestart, ProjectionClockReversal:
	default:
		reason = ProjectionGap
	}
	if p.state.ContinuityRevision == math.MaxUint64 {
		p.state.Exhausted = true
	} else {
		p.state.ContinuityRevision++
	}
	p.unknown(reason)
	p.changed()
}

func (p *ObservationProjection) intake(e ProjectionEvent, now ProjectionClock) (bool, error) {
	if e.state.Selection != p.state.Selection {
		return false, ErrChanged
	}
	if !e.valid {
		return false, ErrInvalid
	}
	if !p.advance(now) {
		return false, ErrChanged
	}
	if e.state.Received.Elapsed > now.Elapsed {
		p.unknown(ProjectionCausality)
		p.changed()
		return false, ErrChanged
	}
	if p.state.Exhausted {
		return false, ErrLimit
	}
	if old, exists := p.seen[e.key]; exists {
		if old.fingerprint != e.fingerprint {
			p.unknown(ProjectionConflict)
			p.changed()
			return false, ErrChanged
		}
		return false, nil
	}
	if len(p.seen) >= MaxProjectionEvents {
		p.state.Exhausted = true
		p.unknown(ProjectionCapacity)
		p.changed()
		return false, ErrLimit
	}
	p.seen[e.key] = projectionReceipt{fingerprint: e.fingerprint}
	p.state.LastEvent = e.state
	p.state.HasLastEvent = true
	p.changed()
	return true, nil
}

// ObserveUnbound retains a bounded last arrival only. It cannot establish a
// current interaction, causal anchor, last waiting or Attention.
func (p *ObservationProjection) ObserveUnbound(e ProjectionEvent, now ProjectionClock) error {
	if !e.valid {
		return ErrInvalid
	}
	if e.state.Selection != p.state.Selection {
		return ErrChanged
	}
	if !p.advance(now) {
		return ErrChanged
	}
	if e.state.Received.Elapsed > now.Elapsed {
		return ErrChanged
	}
	// Untrusted last-arrival metadata never consumes or consults the native
	// causal ledger. Even native-ledger exhaustion must not stop capture.
	p.state.LastEvent = e.state
	p.state.HasLastEvent = true
	p.changed()
	return nil
}

// ReconcileNative consumes already verified adapter evidence. Duplicate events
// cannot renew even expired knowledge. The native adapter must independently
// verify original intake/continuity; unbound history is never its proof source.
func (p *ObservationProjection) ReconcileNative(n NativeProjectionEvidence, now ProjectionClock) error {
	if n.Event.state.Selection != p.state.Selection {
		return ErrChanged
	}
	if n.Revision != p.state.ContinuityRevision {
		p.unknown(ProjectionCausality)
		p.changed()
		return ErrChanged
	}
	if n.Sequence == 0 || !observationCorrelation(n.PreviousEventID, true) || n.Event.state.NativeSessionID == "" {
		p.unknown(ProjectionCausality)
		p.changed()
		return ErrChanged
	}
	fresh, err := p.intake(n.Event, now)
	if err != nil {
		return err
	}
	old := p.seen[n.Event.key]
	if !fresh {
		if old.native && (old.sequence != n.Sequence || old.previous != n.PreviousEventID) {
			p.unknown(ProjectionConflict)
			p.changed()
			return ErrChanged
		}
		return nil
	}
	old.native = true
	old.sequence = n.Sequence
	old.previous = n.PreviousEventID
	p.seen[n.Event.key] = old
	e := n.Event.state
	if p.sequence > 0 && (e.Source != p.source || e.NativeSessionID != p.nativeSession) {
		p.unknown(ProjectionCausality)
		p.changed()
		return ErrChanged
	}
	if n.Sequence < p.sequence {
		return nil
	}
	if n.Sequence == p.sequence {
		p.unknown(ProjectionCausality)
		p.changed()
		return ErrChanged
	}
	causal := n.PreviousEventID == p.anchorID
	p.sequence = n.Sequence
	p.anchorID = e.EventID
	p.nativeSession = e.NativeSessionID
	p.source = e.Source
	// Remember the highest independently verified native frontier even across
	// a missing edge. Older arrivals cannot restore knowledge; only a NEW fresh
	// event correctly chained to this frontier may restore it. Never replay B
	// after delayed A to manufacture freshness.
	if !causal {
		p.unknown(ProjectionCausality)
		p.changed()
		return ErrChanged
	}
	if e.Kind == ProjectionWaiting {
		p.state.LastWaiting = e
		p.state.HasLastWaiting = true
		p.state.LastWaitingStale = true
	}
	if p.state.Process != RunLive {
		p.unknown(ProjectionProcessInactive)
		p.changed()
		return nil
	}
	if now.Elapsed-e.Received.Elapsed >= InteractionTTL {
		p.unknown(ProjectionExpired)
		p.changed()
		return nil
	}
	previous := p.state.Interaction
	attention := false
	p.acceptedAt = e.Received.Elapsed
	switch e.Kind {
	case ProjectionWorking, ProjectionWaiting:
		p.state.Interaction = e.Kind
		p.state.Reason = ""
		p.state.LastWaitingStale = p.state.HasLastWaiting && e.Kind != ProjectionWaiting
		if e.Kind == ProjectionWaiting && previous != ProjectionWaiting {
			attention = true
		}
	default:
		p.unknown(ProjectionCausality)
	}
	p.changed()
	if attention && !p.state.Exhausted {
		p.state.Attention = ProjectionAttention{Source: e.Source, EventID: e.EventID, Revision: p.state.Version}
	}
	return nil
}

// UpdateProcess is called only from the process owner, never from telemetry.
func (p *ObservationProjection) UpdateProcess(s RunState) error {
	if !projectionProcess(s) {
		return ErrInvalid
	}
	if p.state.Process == RunExited && s != RunExited || p.state.Process == RunLive && s == RunReserved {
		return ErrChanged
	}
	if p.state.Process != s {
		p.state.Process = s
		if s != RunLive {
			p.unknown(ProjectionProcessInactive)
		}
		p.changed()
	}
	return nil
}

// UpdateCapture changes only the original-capture health axis.
func (p *ObservationProjection) UpdateCapture(s CaptureHealth) error {
	switch s {
	case CaptureUnknown, CaptureHealthy, CaptureDegraded:
	default:
		return ErrInvalid
	}
	if s != p.state.CaptureHealth {
		p.state.CaptureHealth = s
		p.changed()
	}
	return nil
}

// UpdateConnection changes only the transport-health axis.
func (p *ObservationProjection) UpdateConnection(s ConnectionHealth) error {
	switch s {
	case ConnectionUnknown, ConnectionConnected, ConnectionDisconnected:
	default:
		return ErrInvalid
	}
	if s != p.state.ConnectionHealth {
		p.state.ConnectionHealth = s
		p.changed()
	}
	return nil
}
