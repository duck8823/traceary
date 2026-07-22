package model

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// UsageObservationDescriptor is immutable semantic identity shared by pending
// and finalized representations of one authoritative observation.
type UsageObservationDescriptor struct {
	observationID    types.UsageObservationID
	sessionID        types.SessionID
	source           types.UsageSource
	scope            types.UsageScope
	accounting       types.UsageAccounting
	observedAt       time.Time
	snapshotSeries   string
	snapshotRevision int64
	supersedesID     types.Optional[types.UsageObservationID]
	runIdentity      types.Optional[types.RunIdentity]
}

// NewUsageObservationDescriptor creates identity for a call or run. Session
// snapshots use NewUsageSnapshotDescriptor so their chain cannot be omitted.
func NewUsageObservationDescriptor(
	observationID types.UsageObservationID,
	sessionID types.SessionID,
	source types.UsageSource,
	scope types.UsageScope,
	accounting types.UsageAccounting,
	observedAt time.Time,
) (UsageObservationDescriptor, error) {
	if scope != types.UsageScopeCall && scope != types.UsageScopeRun {
		return UsageObservationDescriptor{}, xerrors.Errorf("call/run descriptor has unsupported scope %q", scope)
	}
	if accounting != types.UsageAccountingAdditive && accounting != types.UsageAccountingExcluded {
		return UsageObservationDescriptor{}, xerrors.Errorf("call/run descriptor has unsupported accounting %q", accounting)
	}
	descriptor := UsageObservationDescriptor{
		observationID: observationID,
		sessionID:     sessionID,
		source:        source,
		scope:         scope,
		accounting:    accounting,
		observedAt:    observedAt,
		supersedesID:  types.None[types.UsageObservationID](),
		runIdentity:   types.None[types.RunIdentity](),
	}
	if err := descriptor.validate(); err != nil {
		return UsageObservationDescriptor{}, err
	}
	return descriptor, nil
}

// NewUsageObservationDescriptorWithRunIdentity creates call/run identity
// explicitly attributed to a previously recorded host run.
func NewUsageObservationDescriptorWithRunIdentity(
	observationID types.UsageObservationID,
	sessionID types.SessionID,
	source types.UsageSource,
	scope types.UsageScope,
	accounting types.UsageAccounting,
	observedAt time.Time,
	runIdentity types.RunIdentity,
) (UsageObservationDescriptor, error) {
	descriptor, err := NewUsageObservationDescriptor(observationID, sessionID, source, scope, accounting, observedAt)
	if err != nil {
		return UsageObservationDescriptor{}, err
	}
	descriptor.runIdentity = types.Some(runIdentity)
	if err := descriptor.validate(); err != nil {
		return UsageObservationDescriptor{}, err
	}
	return descriptor, nil
}

// NewUsageSnapshotDescriptor creates identity for one immutable cumulative
// session snapshot revision.
func NewUsageSnapshotDescriptor(
	observationID types.UsageObservationID,
	sessionID types.SessionID,
	source types.UsageSource,
	series string,
	revision int64,
	supersedesID types.Optional[types.UsageObservationID],
	observedAt time.Time,
) (UsageObservationDescriptor, error) {
	descriptor := UsageObservationDescriptor{
		observationID:    observationID,
		sessionID:        sessionID,
		source:           source,
		scope:            types.UsageScopeSessionSnapshot,
		accounting:       types.UsageAccountingLatestSnapshot,
		observedAt:       observedAt,
		snapshotSeries:   strings.TrimSpace(series),
		snapshotRevision: revision,
		supersedesID:     supersedesID,
		runIdentity:      types.None[types.RunIdentity](),
	}
	if err := descriptor.validate(); err != nil {
		return UsageObservationDescriptor{}, err
	}
	return descriptor, nil
}

func (d UsageObservationDescriptor) validate() error {
	if _, err := types.UsageObservationIDFrom(d.observationID.String()); err != nil {
		return xerrors.Errorf("invalid usage observation descriptor: %w", err)
	}
	if _, err := types.SessionIDFrom(d.sessionID.String()); err != nil {
		return xerrors.Errorf("invalid usage observation descriptor: %w", err)
	}
	if _, err := types.UsageSourceOf(d.source.Host(), d.source.Name(), d.source.Version(), d.source.Provider(), d.source.Model()); err != nil {
		return xerrors.Errorf("invalid usage observation descriptor: %w", err)
	}
	if _, err := types.UsageScopeFrom(d.scope.String()); err != nil {
		return xerrors.Errorf("invalid usage observation descriptor: %w", err)
	}
	if _, err := types.UsageAccountingFrom(d.accounting.String()); err != nil {
		return xerrors.Errorf("invalid usage observation descriptor: %w", err)
	}
	if d.observedAt.IsZero() {
		return xerrors.Errorf("usage observation timestamp must not be zero")
	}
	if d.scope == types.UsageScopeSessionSnapshot {
		if _, present := d.runIdentity.Value(); present {
			return xerrors.Errorf("session snapshot cannot carry run identity")
		}
		if d.accounting != types.UsageAccountingLatestSnapshot || d.snapshotSeries == "" || d.snapshotRevision < 1 {
			return xerrors.Errorf("session snapshot requires latest-snapshot accounting, series, and positive revision")
		}
		if len(d.snapshotSeries) > 512 {
			return xerrors.Errorf("usage snapshot series must not exceed 512 bytes")
		}
	} else if d.snapshotSeries != "" || d.snapshotRevision != 0 {
		return xerrors.Errorf("call/run descriptor must not carry snapshot metadata")
	} else if _, present := d.supersedesID.Value(); present {
		return xerrors.Errorf("call/run descriptor must not supersede a snapshot")
	}
	if predecessor, present := d.supersedesID.Value(); present {
		if _, err := types.UsageObservationIDFrom(predecessor.String()); err != nil {
			return xerrors.Errorf("invalid superseded observation ID: %w", err)
		}
		if predecessor == d.observationID {
			return xerrors.Errorf("usage snapshot cannot supersede itself")
		}
	}
	if runIdentity, present := d.runIdentity.Value(); present {
		validated, err := types.RunIdentityFrom(runIdentity.Host(), runIdentity.RunID())
		if err != nil || validated != runIdentity {
			return xerrors.Errorf("invalid usage run identity")
		}
		if runIdentity.Host() != d.source.Host() {
			return xerrors.Errorf("usage run identity host must match source host")
		}
	}
	return nil
}

func (d UsageObservationDescriptor) equivalent(other UsageObservationDescriptor) bool {
	if d.observationID != other.observationID || d.sessionID != other.sessionID || d.source != other.source ||
		d.scope != other.scope || d.accounting != other.accounting || d.snapshotSeries != other.snapshotSeries ||
		d.snapshotRevision != other.snapshotRevision || !d.observedAt.Equal(other.observedAt) {
		return false
	}
	currentPredecessor, currentPresent := d.supersedesID.Value()
	otherPredecessor, otherPresent := other.supersedesID.Value()
	if currentPresent != otherPresent || (currentPresent && currentPredecessor != otherPredecessor) {
		return false
	}
	currentRun, currentRunPresent := d.runIdentity.Value()
	otherRun, otherRunPresent := other.runIdentity.Value()
	return currentRunPresent == otherRunPresent && (!currentRunPresent || currentRun == otherRun)
}

// ObservationID returns the adapter-owned authoritative identity.
func (d UsageObservationDescriptor) ObservationID() types.UsageObservationID {
	return d.observationID
}

// SessionID returns the correlated Traceary or host session identity.
func (d UsageObservationDescriptor) SessionID() types.SessionID { return d.sessionID }

// Source returns the versioned local usage source.
func (d UsageObservationDescriptor) Source() types.UsageSource { return d.source }

// Scope returns call, run, or session-snapshot granularity.
func (d UsageObservationDescriptor) Scope() types.UsageScope { return d.scope }

// Accounting returns the explicit aggregate behavior.
func (d UsageObservationDescriptor) Accounting() types.UsageAccounting { return d.accounting }

// ObservedAt returns the authoritative source boundary timestamp.
func (d UsageObservationDescriptor) ObservedAt() time.Time { return d.observedAt }

// SnapshotSeries returns the immutable snapshot series key, when applicable.
func (d UsageObservationDescriptor) SnapshotSeries() string { return d.snapshotSeries }

// SnapshotRevision returns the positive ingest revision, when applicable.
func (d UsageObservationDescriptor) SnapshotRevision() int64 { return d.snapshotRevision }

// SupersedesID returns the predecessor snapshot identity, when applicable.
func (d UsageObservationDescriptor) SupersedesID() types.Optional[types.UsageObservationID] {
	return d.supersedesID
}

// RunIdentity returns the namespaced run attribution, when known.
func (d UsageObservationDescriptor) RunIdentity() types.Optional[types.RunIdentity] {
	return d.runIdentity
}

// UsageObservation is the aggregate root for idempotent usage accounting.
type UsageObservation struct {
	descriptor   UsageObservationDescriptor
	status       types.UsageObservationStatus
	counters     types.UsageCounters
	cost         types.UsageCost
	terminalCode types.Optional[types.UsageTerminalCode]
	finalizedAt  types.Optional[time.Time]
}

// NewPendingUsageObservation creates an open call/run with no numeric usage.
func NewPendingUsageObservation(descriptor UsageObservationDescriptor) (*UsageObservation, error) {
	return UsageObservationOf(
		descriptor,
		types.UsageObservationPending,
		types.UnknownUsageCounters(),
		types.UnknownUsageCost(),
		types.None[types.UsageTerminalCode](),
		types.None[time.Time](),
	)
}

// NewFinalizedUsageObservation creates a terminal observation. Every counter
// dimension and the cost must be explicitly known or unavailable.
func NewFinalizedUsageObservation(
	descriptor UsageObservationDescriptor,
	counters types.UsageCounters,
	cost types.UsageCost,
	terminalCode types.UsageTerminalCode,
	finalizedAt time.Time,
) (*UsageObservation, error) {
	return UsageObservationOf(
		descriptor,
		types.UsageObservationFinalized,
		counters,
		cost,
		types.Some(terminalCode),
		types.Some(finalizedAt),
	)
}

// UsageObservationOf restores a persisted aggregate while rechecking every
// domain invariant.
func UsageObservationOf(
	descriptor UsageObservationDescriptor,
	status types.UsageObservationStatus,
	counters types.UsageCounters,
	cost types.UsageCost,
	terminalCode types.Optional[types.UsageTerminalCode],
	finalizedAt types.Optional[time.Time],
) (*UsageObservation, error) {
	observation := &UsageObservation{
		descriptor:   descriptor,
		status:       status,
		counters:     counters,
		cost:         cost,
		terminalCode: terminalCode,
		finalizedAt:  finalizedAt,
	}
	if err := observation.validate(); err != nil {
		return nil, err
	}
	return observation, nil
}

func (o *UsageObservation) validate() error {
	if o == nil {
		return ErrInvalidUsageObservation
	}
	if err := o.descriptor.validate(); err != nil {
		return xerrors.Errorf("invalid usage observation: %w", err)
	}
	if _, err := types.UsageObservationStatusFrom(o.status.String()); err != nil {
		return xerrors.Errorf("invalid usage observation: %w", err)
	}
	if _, err := types.UsageCountersOf(
		o.counters.Input(), o.counters.CachedInput(), o.counters.CacheWriteInput(),
		o.counters.Output(), o.counters.ReasoningOutput(), o.counters.Total(),
	); err != nil {
		return xerrors.Errorf("invalid usage observation counters: %w", err)
	}
	amount := types.None[int64]()
	if value, present := o.cost.AmountMicros(); present {
		amount = types.Some(value)
	}
	if _, err := types.UsageCostFrom(
		o.cost.State().String(), amount, o.cost.Currency(), o.cost.Origin().String(), o.cost.PriceTableVersion(),
	); err != nil {
		return xerrors.Errorf("invalid usage observation cost: %w", err)
	}

	if o.status == types.UsageObservationPending {
		if o.descriptor.scope == types.UsageScopeSessionSnapshot {
			return xerrors.Errorf("session snapshot cannot be pending: %w", ErrInvalidUsageObservation)
		}
		if !allUsageValuesHaveState(o.counters, types.UsageValueUnknown) || o.cost.State() != types.UsageCostUnknown {
			return xerrors.Errorf("pending usage observation must contain only unknown values: %w", ErrInvalidUsageObservation)
		}
		if _, present := o.terminalCode.Value(); present {
			return xerrors.Errorf("pending usage observation must not have a terminal code: %w", ErrInvalidUsageObservation)
		}
		if _, present := o.finalizedAt.Value(); present {
			return xerrors.Errorf("pending usage observation must not have a final timestamp: %w", ErrInvalidUsageObservation)
		}
		return nil
	}

	if o.counters.Availability() == types.UsageAvailabilityUnknown || o.cost.State() == types.UsageCostUnknown {
		return xerrors.Errorf("finalized usage observation must not contain unknown values: %w", ErrInvalidUsageObservation)
	}
	code, present := o.terminalCode.Value()
	if !present {
		return xerrors.Errorf("finalized usage observation requires a terminal code: %w", ErrInvalidUsageObservation)
	}
	if _, err := types.UsageTerminalCodeFrom(code.String()); err != nil {
		return xerrors.Errorf("invalid usage observation terminal code: %w", err)
	}
	finalizedAt, present := o.finalizedAt.Value()
	if !present || finalizedAt.IsZero() || finalizedAt.Before(o.descriptor.observedAt) {
		return xerrors.Errorf("finalized usage observation has an invalid final timestamp: %w", ErrInvalidUsageObservation)
	}
	return nil
}

func allUsageValuesHaveState(counters types.UsageCounters, state types.UsageValueState) bool {
	values := []types.UsageValue{
		counters.Input(), counters.CachedInput(), counters.CacheWriteInput(),
		counters.Output(), counters.ReasoningOutput(), counters.Total(),
	}
	for _, value := range values {
		if value.State() != state {
			return false
		}
	}
	return true
}

// UsageObservationTransition is the observable result of Record/Reconcile.
type UsageObservationTransition string

const (
	// UsageObservationTransitionApplied means a row or terminal state was written.
	UsageObservationTransitionApplied UsageObservationTransition = "applied"
	// UsageObservationTransitionAlreadyApplied means an exact replay changed nothing.
	UsageObservationTransitionAlreadyApplied UsageObservationTransition = "already_applied"
)

// Reconcile applies one semantically matching pending-to-finalized transition.
// Replays are no-ops and all other changes fail closed.
func (o *UsageObservation) Reconcile(proposed *UsageObservation) (UsageObservationTransition, error) {
	if o == nil || proposed == nil {
		return "", ErrInvalidUsageObservation
	}
	if !o.descriptor.equivalent(proposed.descriptor) {
		return "", newUsageObservationConflict(o.descriptor.observationID, "identity metadata")
	}
	if o.status == types.UsageObservationPending {
		switch proposed.status {
		case types.UsageObservationPending:
			return UsageObservationTransitionAlreadyApplied, nil
		case types.UsageObservationFinalized:
			o.status = proposed.status
			o.counters = proposed.counters
			o.cost = proposed.cost
			o.terminalCode = proposed.terminalCode
			o.finalizedAt = proposed.finalizedAt
			return UsageObservationTransitionApplied, nil
		}
	}
	if o.status == types.UsageObservationFinalized {
		if proposed.status == types.UsageObservationPending {
			return UsageObservationTransitionAlreadyApplied, nil
		}
		currentCode, _ := o.terminalCode.Value()
		proposedCode, _ := proposed.terminalCode.Value()
		currentFinalizedAt, _ := o.finalizedAt.Value()
		proposedFinalizedAt, _ := proposed.finalizedAt.Value()
		if o.counters == proposed.counters && o.cost == proposed.cost && currentCode == proposedCode &&
			currentFinalizedAt.Equal(proposedFinalizedAt) {
			return UsageObservationTransitionAlreadyApplied, nil
		}
		return "", newUsageObservationConflict(o.descriptor.observationID, "terminal accounting")
	}
	return "", ErrInvalidUsageObservation
}

// ValidateSnapshotSuccessor verifies that this observation can extend the
// supplied current snapshot head. A nil head is valid only for the first
// revision of a series and therefore requires no predecessor.
func (o *UsageObservation) ValidateSnapshotSuccessor(head *UsageObservation) error {
	if o == nil || o.descriptor.scope != types.UsageScopeSessionSnapshot || o.status != types.UsageObservationFinalized {
		return ErrInvalidUsageObservation
	}
	predecessor, hasPredecessor := o.descriptor.supersedesID.Value()
	if head == nil {
		if hasPredecessor {
			return newUsageObservationConflict(o.descriptor.observationID, "missing snapshot predecessor")
		}
		return nil
	}
	if head.descriptor.scope != types.UsageScopeSessionSnapshot ||
		head.descriptor.snapshotSeries != o.descriptor.snapshotSeries ||
		head.status != types.UsageObservationFinalized {
		return newUsageObservationConflict(o.descriptor.observationID, "snapshot series")
	}
	if head.descriptor.sessionID != o.descriptor.sessionID || head.descriptor.source != o.descriptor.source {
		return newUsageObservationConflict(o.descriptor.observationID, "snapshot lineage")
	}
	if !hasPredecessor || predecessor != head.descriptor.observationID {
		return newUsageObservationConflict(o.descriptor.observationID, "snapshot predecessor")
	}
	if o.descriptor.snapshotRevision <= head.descriptor.snapshotRevision {
		return newUsageObservationConflict(o.descriptor.observationID, "snapshot revision")
	}
	return nil
}

// Descriptor returns immutable semantic identity.
func (o *UsageObservation) Descriptor() UsageObservationDescriptor { return o.descriptor }

// Status returns pending or finalized lifecycle state.
func (o *UsageObservation) Status() types.UsageObservationStatus { return o.status }

// Counters returns provider-neutral token dimensions.
func (o *UsageObservation) Counters() types.UsageCounters { return o.counters }

// Cost returns explicit availability and provenance for monetary cost.
func (o *UsageObservation) Cost() types.UsageCost { return o.cost }

// TerminalCode returns the normalized terminal code when finalized.
func (o *UsageObservation) TerminalCode() types.Optional[types.UsageTerminalCode] {
	return o.terminalCode
}

// FinalizedAt returns the first accepted terminal timestamp.
func (o *UsageObservation) FinalizedAt() types.Optional[time.Time] { return o.finalizedAt }

// UsageObservationConflictError reports a fail-closed semantic conflict
// without including host body or free-form terminal data.
type UsageObservationConflictError struct {
	observationID types.UsageObservationID
	field         string
}

func newUsageObservationConflict(id types.UsageObservationID, field string) *UsageObservationConflictError {
	return &UsageObservationConflictError{observationID: id, field: field}
}

func (e *UsageObservationConflictError) Error() string {
	return fmt.Sprintf("usage observation %q conflicts on %s", e.observationID, e.field)
}

func (e *UsageObservationConflictError) Unwrap() error { return ErrConflictingUsageObservation }

// ObservationID returns the identity whose proposed semantics conflicted.
func (e *UsageObservationConflictError) ObservationID() types.UsageObservationID {
	return e.observationID
}
