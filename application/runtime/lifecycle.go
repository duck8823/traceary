package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
	"math"
	"sync"
	"time"
)

// ErrShutdownUnknown forbids automatic retry after a possibly committed decision.
var ErrShutdownUnknown = errors.New("daemon shutdown outcome uncertain; inspect the original preview without replay")

// ErrLifecycleCommitUncertain is a portable ambiguous durable-intent marker.
var ErrLifecycleCommitUncertain = errors.New("runtime lifecycle metadata commit uncertain")

// ClosingPTYChild operates on the actual owned descriptor, never a saved PID.
// CloseTerminal joins the sole reader before closing, is idempotent, sends no
// explicit signal, and preserves the separately guarded original-group Stop.
type ClosingPTYChild interface {
	ValidateTerminalClose(context.Context) error
	CloseTerminal(context.Context) error
}

// ShutdownJournal stores only safe plan/effect metadata, independently of core data.
type ShutdownJournal interface {
	ReserveShutdown(context.Context, domain.ShutdownBinding) error
	SaveShutdown(context.Context, ShutdownResult) error
}

// ShutdownPreview is a frozen complete roster. Transport must page it within its
// byte limits, without creating a new preview for each continuation.
type ShutdownPreview struct {
	Binding domain.ShutdownBindingState
	Runs    []domain.RunSnapshot
}

// ShutdownConfirmation is an explicit full-plan work-loss acknowledgment.
type ShutdownConfirmation struct {
	Binding              domain.ShutdownBindingState
	WorkLossAcknowledged bool
}

// ShutdownResult never equates master closure with child/descendant termination.
type ShutdownResult struct {
	Binding         domain.ShutdownBindingState
	Outcome         domain.ShutdownOutcome
	ClosedTerminals int
}
type shutdownTicket struct {
	confirmation ShutdownConfirmation
	result       ShutdownResult
	err          error
	done         chan struct{}
}

func (s *Runtime) shutdownRosterLocked() []domain.RunSnapshot {
	runs := s.listLocked()
	result := make([]domain.RunSnapshot, 0, len(runs))
	for _, r := range runs {
		if r.State != domain.RunExited || r.PublicationUncertain {
			result = append(result, r)
		}
	}
	return result
}
func (s *Runtime) shutdownBindingLocked(id string) domain.ShutdownBindingState {
	runs := s.shutdownRosterLocked()
	rows := make([]ShutdownPreviewRun, 0, len(runs))
	for _, run := range runs {
		rows = append(rows, ShutdownPreviewRunFrom(run))
	}
	digest, _ := ShutdownRosterDigest(rows) // owned snapshots already satisfy these invariants

	publishing, reconciling := 0, 0
	for _, ticket := range s.tickets {
		if ticket.starting && !ticket.complete {
			if _, exists := s.runs[ticket.descriptor.Snapshot().Identity.Run]; exists {
				publishing++
			}
		}
	}
	for _, run := range s.runs {
		if run.descriptor.Snapshot().State == domain.RunExited {
			select {
			case <-run.done:
			default:
				reconciling++
			}
		}
	}
	return domain.ShutdownBindingState{Epoch: s.epoch, PreviewID: id, Revision: s.revision, RosterDigest: digest, TotalRuns: len(runs), PendingLaunches: s.pending + publishing, PendingControls: s.controlsPending, PendingReconciliations: reconciling}
}

// PreviewShutdown has no native effect and does not freeze admissions.
func (s *Runtime) PreviewShutdown(ctx context.Context) (ShutdownPreview, error) {
	if err := ctx.Err(); err != nil {
		return ShutdownPreview{}, xerrors.Errorf("preview daemon shutdown: %w", err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ShutdownPreview{}, domain.ErrUnsafe
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdownPreparing || s.shutdownCommitted || s.lifecycleStarting || s.lifecyclePending > 0 || s.lifecycleUncertain {
		return ShutdownPreview{}, ErrRuntimeBusy
	}
	if s.revisionExhausted || s.revision == math.MaxUint64 {
		return ShutdownPreview{}, domain.ErrLimit
	}
	preview := ShutdownPreview{Binding: s.shutdownBindingLocked(hex.EncodeToString(nonce[:])), Runs: s.shutdownRosterLocked()}
	s.shutdownPreview = &preview
	return copyShutdownPreview(preview), nil
}
func copyShutdownPreview(p ShutdownPreview) ShutdownPreview {
	p.Runs = append([]domain.RunSnapshot{}, p.Runs...)
	return p
}

// ReadShutdownPreview reads an existing frozen plan without replacing it.
func (s *Runtime) ReadShutdownPreview(binding domain.ShutdownBindingState) (ShutdownPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdownPreview == nil || s.shutdownPreview.Binding != binding || s.shutdownBindingLocked(binding.PreviewID) != binding {
		return ShutdownPreview{}, domain.ErrChanged
	}
	return copyShutdownPreview(*s.shutdownPreview), nil
}

// CancelShutdown discards a preview before confirm admission. Confirm acceptance
// is the cancellation boundary; cancel never promises to restore closed PTYs.
func (s *Runtime) CancelShutdown(binding domain.ShutdownBindingState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdownPreview == nil || s.shutdownPreview.Binding != binding || s.shutdownPreparing || s.shutdownCommitted || s.lifecycleUncertain {
		return domain.ErrChanged
	}
	s.shutdownPreview = nil
	return nil
}

// ConfirmShutdown single-flights one exact explicitly acknowledged plan. Caller
// cancellation cannot abandon ownership or automatically replay master closure.
func (s *Runtime) ConfirmShutdown(ctx context.Context, request ShutdownConfirmation) (ShutdownResult, error) {
	if err := ctx.Err(); err != nil {
		return ShutdownResult{}, xerrors.Errorf("confirm daemon shutdown: %w", err)
	}
	binding, err := domain.ShutdownBindingFrom(request.Binding)
	if err != nil {
		return ShutdownResult{}, xerrors.Errorf("validate shutdown binding: %w", err)
	}
	if !request.WorkLossAcknowledged {
		return ShutdownResult{}, domain.ErrInvalid
	}
	s.mu.Lock()
	if ticket := s.shutdownTickets[request.Binding.PreviewID]; ticket != nil {
		if ticket.confirmation != request {
			s.mu.Unlock()
			return ShutdownResult{}, domain.ErrChanged
		}
		s.mu.Unlock()
		return s.awaitShutdown(ctx, ticket)
	}
	if s.shutdownPreview == nil || s.shutdownPreview.Binding != request.Binding {
		s.mu.Unlock()
		return ShutdownResult{}, domain.ErrChanged
	}
	if err = binding.Confirm(s.shutdownBindingLocked(request.Binding.PreviewID), true); err != nil {
		s.mu.Unlock()
		return ShutdownResult{}, xerrors.Errorf("validate current shutdown roster: %w", err)
	}
	if s.revisionExhausted || s.revision == math.MaxUint64 {
		s.mu.Unlock()
		return ShutdownResult{}, domain.ErrLimit
	}
	if s.shutdownPreparing || s.shutdownCommitted || s.lifecycleStarting || s.lifecyclePending > 0 || s.hasUncertainLocked() {
		s.mu.Unlock()
		return ShutdownResult{}, ErrRuntimeBusy
	}
	journal, ok := s.journal.(ShutdownJournal)
	if !ok {
		s.mu.Unlock()
		return ShutdownResult{}, domain.ErrUnsupported
	}
	children := make([]ClosingPTYChild, 0, len(s.shutdownPreview.Runs))
	snapshots := append([]domain.RunSnapshot{}, s.shutdownPreview.Runs...)
	for _, snapshot := range snapshots {
		run := s.runs[snapshot.Identity.Run]
		if run == nil {
			s.mu.Unlock()
			return ShutdownResult{}, domain.ErrChanged
		}
		child, capable := run.child.(ClosingPTYChild)
		if !capable {
			s.mu.Unlock()
			return ShutdownResult{}, domain.ErrUnsupported
		}
		children = append(children, child)
	}
	if len(s.shutdownTickets) >= domain.MaxLaunchRequests {
		s.mu.Unlock()
		return ShutdownResult{}, domain.ErrLimit
	}
	if s.shutdownTickets == nil {
		s.shutdownTickets = make(map[string]*shutdownTicket)
	}
	ticket := &shutdownTicket{confirmation: request, result: ShutdownResult{Binding: request.Binding, Outcome: domain.ShutdownPending}, done: make(chan struct{})}
	s.shutdownTickets[request.Binding.PreviewID] = ticket
	s.shutdownPreparing = true
	s.lifecyclePending++
	s.changedLocked()
	reservationRevision := s.revision
	s.mu.Unlock()
	go s.performShutdown(context.WithoutCancel(ctx), ticket, binding, journal, children, snapshots, reservationRevision)
	return s.awaitShutdown(ctx, ticket)
}
func (s *Runtime) awaitShutdown(ctx context.Context, ticket *shutdownTicket) (ShutdownResult, error) {
	select {
	case <-ticket.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return ticket.result, ticket.err
	case <-ctx.Done():
		s.mu.Lock()
		defer s.mu.Unlock()
		if ticket.result.Outcome != domain.ShutdownPending {
			return ticket.result, ticket.err
		}
		result := ticket.result
		result.Outcome = domain.ShutdownUnknown
		return result, ErrShutdownUnknown
	}
}
func (s *Runtime) hasUncertainLocked() bool {
	if s.lifecycleUncertain {
		return true
	}
	for _, ticket := range s.shutdownTickets {
		if ticket.result.Outcome == domain.ShutdownUnknown {
			return true
		}
	}
	for _, run := range s.runs {
		if run.descriptor.Snapshot().PublicationUncertain {
			return true
		}
	}
	for _, ticket := range s.tickets {
		if ticket.result.PublicationUncertain {
			return true
		}
	}
	return false
}
func (s *Runtime) finishShutdown(ticket *shutdownTicket, result ShutdownResult, err error, committed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket.result = result
	ticket.err = err
	s.shutdownPreparing = false
	if result.Outcome == domain.ShutdownUnknown {
		// Metadata uncertainty is independent of live/launch publication. Even
		// an empty roster cannot turn an ambiguous durable decision into clean.
		s.lifecycleUncertain = true
		s.disabled = true
	}
	if committed {
		s.shutdownCommitted = true
		s.disabled = true
	}
	s.lifecyclePending--
	s.changedLocked()
	close(ticket.done)
}
func (s *Runtime) performShutdown(parent context.Context, ticket *shutdownTicket, binding domain.ShutdownBinding, journal ShutdownJournal, children []ClosingPTYChild, snapshots []domain.RunSnapshot, reservationRevision uint64) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	result := ticket.result
	reject := func(err error) {
		result.Outcome = domain.ShutdownRejected
		s.finishShutdown(ticket, result, err, false)
	}
	for i, child := range children {
		snapshot := snapshots[i]
		if err := s.registry.Revalidate(ctx, snapshot.Target, string(snapshot.Profile.ID())); err != nil {
			reject(err)
			return
		}
		profile, err := s.profiles.LookupProfile(ctx, snapshot.Profile.ID())
		if err != nil {
			reject(err)
			return
		}
		if profile != snapshot.Profile {
			reject(domain.ErrChanged)
			return
		}
		if err = child.ValidateTerminalClose(ctx); err != nil {
			reject(err)
			return
		}
	}
	s.mu.Lock()
	changed := s.revision != reservationRevision || s.hasUncertainLocked()
	s.mu.Unlock()
	if changed {
		reject(domain.ErrChanged)
		return
	}
	if err := journal.ReserveShutdown(ctx, binding); err != nil {
		if errors.Is(err, ErrLifecycleCommitUncertain) || ctx.Err() != nil {
			result.Outcome = domain.ShutdownUnknown
			// No master close was attempted. Freeze new effects, but keep the
			// owner/management receipt available instead of asserting a known
			// committed shutdown and automatically exiting an empty daemon.
			s.finishShutdown(ticket, result, ErrShutdownUnknown, false)
		} else {
			reject(err)
		}
		return
	}
	// Durable intent precedes all effects. There is no implicit signal or retry.
	s.mu.Lock()
	s.shutdownCommitted = true
	s.disabled = true
	s.changedLocked()
	s.mu.Unlock()
	var mu sync.Mutex
	var joined sync.WaitGroup
	for _, child := range children {
		joined.Add(1)
		go func(c ClosingPTYChild) {
			defer joined.Done()
			err := c.CloseTerminal(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Outcome = domain.ShutdownUnknown
			} else {
				result.ClosedTerminals++
			}
		}(child)
	}
	joined.Wait() // retain ownership if an adapter violates its cancellation contract
	if result.Outcome != domain.ShutdownUnknown {
		result.Outcome = domain.ShutdownDraining
	}
	saveCtx, saveCancel := context.WithTimeout(parent, 2*time.Second)
	err := journal.SaveShutdown(saveCtx, result)
	saveCancel()
	if err != nil || result.Outcome == domain.ShutdownUnknown {
		result.Outcome = domain.ShutdownUnknown
		s.finishShutdown(ticket, result, ErrShutdownUnknown, true)
		return
	}
	s.finishShutdown(ticket, result, nil, true)
}

// ShutdownReceipt inspects the original decision without submitting confirmation
// again. Pending/unknown are not proof of closure, exit, or permission to replay.
func (s *Runtime) ShutdownReceipt(binding domain.ShutdownBindingState) (ShutdownResult, error) {
	if _, err := domain.ShutdownBindingFrom(binding); err != nil {
		return ShutdownResult{}, xerrors.Errorf("validate shutdown receipt binding: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket := s.shutdownTickets[binding.PreviewID]
	if ticket == nil {
		return ShutdownResult{}, domain.ErrNotFound
	}
	if ticket.confirmation.Binding != binding {
		return ShutdownResult{}, domain.ErrChanged
	}
	return ticket.result, nil
}

// AwaitShutdownConfirmation waits for a known-reserved work-loss decision.
// Pre-reservation uncertainty retains a frozen owner for inspection instead.
// This never initiates shutdown or equates closure with process exit.
func (s *Runtime) AwaitShutdownConfirmation(ctx context.Context) error {
	for {
		s.mu.Lock()
		committed := s.shutdownCommitted
		changed := s.changed
		s.mu.Unlock()
		if committed {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return xerrors.Errorf("await daemon shutdown decision: %w", ctx.Err())
		}
	}
}

// ShutdownInProgress is admission/capability metadata only.
func (s *Runtime) ShutdownInProgress() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdownPreparing || s.shutdownCommitted || s.lifecycleUncertain
}

// PublishCleanShutdown requires permanently frozen launch admission and actual
// owned-handle/coordinator joins. Historical marker publication is never resume
// permission, nor proof of escaped descendant death or input delivery certainty.
func (s *Runtime) PublishCleanShutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.lifecycleBound {
		s.mu.Unlock()
		return domain.ErrUnsupported
	}
	if !s.disabled || s.pending+s.active+s.controlsPending+s.lifecyclePending != 0 {
		s.mu.Unlock()
		return ErrRuntimeBusy
	}
	if s.hasUncertainLocked() {
		s.mu.Unlock()
		return ErrShutdownUnknown
	}
	repository, ok := s.journal.(LifecycleRepository)
	s.mu.Unlock()
	if !ok {
		return domain.ErrUnsupported
	}
	if err := repository.RecordCleanShutdown(ctx, s.epoch); err != nil {
		return xerrors.Errorf("publish clean daemon shutdown: %w", err)
	}
	return nil
}

// InitializeLifecycle is trusted composition before ANY launch admission. Its
// durable new-epoch marker is opt-in; legacy/custom owners are not silently
// enrolled merely because their journal also implements LifecycleRepository.
// A failed/ambiguous startup permanently refuses new launch admission.
func (s *Runtime) InitializeLifecycle(ctx context.Context) (LifecycleSnapshot, error) {
	s.mu.Lock()
	if s.lifecycleStarting || s.lifecycleBound || s.disabled || len(s.tickets) != 0 || s.shutdownPreparing || s.shutdownCommitted {
		s.mu.Unlock()
		return LifecycleSnapshot{}, ErrRuntimeBusy
	}
	repository, ok := s.journal.(LifecycleRepository)
	if !ok {
		s.mu.Unlock()
		return LifecycleSnapshot{}, domain.ErrUnsupported
	}
	s.lifecycleStarting = true
	s.changedLocked()
	s.mu.Unlock()
	snapshot, err := repository.RecordEpochStart(ctx, s.epoch)
	s.mu.Lock()
	s.lifecycleStarting = false
	if err != nil {
		s.disabled = true
		if errors.Is(err, ErrLifecycleCommitUncertain) {
			s.lifecycleUncertain = true
		}
	} else {
		s.lifecycleBound = true
	}
	s.changedLocked()
	s.mu.Unlock()
	if err != nil {
		return LifecycleSnapshot{}, xerrors.Errorf("initialize daemon lifecycle: %w", err)
	}
	return snapshot, nil
}
