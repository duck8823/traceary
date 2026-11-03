package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// Only canonical selections/states are hashed; no terminal, path, PID or input.
	type entry struct {
		Identity  domain.LaunchIdentity
		Worktree  domain.WorktreeID
		Profile   domain.ProfileID
		State     domain.RunState
		Uncertain bool
	}
	runs := s.shutdownRosterLocked()
	roster := make([]entry, 0, len(runs))
	for _, r := range runs {
		roster = append(roster, entry{r.Identity, r.Target.Worktree().ID(), r.Profile.ID(), r.State, r.PublicationUncertain})
	}
	data, _ := json.Marshal(roster) // closed, primitive-only local type cannot fail encoding
	digest := sha256.Sum256(data)
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
	return domain.ShutdownBindingState{Epoch: s.epoch, PreviewID: id, Revision: s.revision, RosterDigest: hex.EncodeToString(digest[:]), TotalRuns: len(runs), PendingLaunches: s.pending + publishing, PendingControls: s.controlsPending, PendingReconciliations: reconciling}
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
	if s.shutdownPreparing || s.shutdownCommitted {
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
	if s.shutdownPreview == nil || s.shutdownPreview.Binding != binding || s.shutdownPreparing || s.shutdownCommitted {
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
	if s.shutdownPreparing || s.shutdownCommitted || s.hasUncertainLocked() {
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
			s.finishShutdown(ticket, result, ErrShutdownUnknown, true)
		} else {
			reject(err)
		}
		return
	}
	// Durable intent precedes all effects. There is no implicit signal or retry.
	s.mu.Lock()
	s.shutdownCommitted = true
	s.disabled = true
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
