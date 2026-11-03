package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
	"math"
	"time"
)

type runControls struct {
	ledger  *domain.ControlLedger
	tickets map[string]*controlTicket
	busy    bool
}
type controlTicket struct {
	intent      domain.ControlIntent
	fingerprint [32]byte
	result      ControlResult
	err         error
	done        chan struct{}
}

func (s *Runtime) controlRunLocked(selection OutputSelection) (*ownedRun, error) {
	run := s.runs[selection.Identity.Run]
	if run == nil {
		return nil, domain.ErrNotFound
	}
	snapshot := run.descriptor.Snapshot()
	expected := OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	if selection != expected {
		return nil, domain.ErrChanged
	}
	return run, nil
}
func (s *Runtime) controlFingerprintLocked(request ControlRequest, data []byte) ([32]byte, error) {
	if !s.controlKeySet {
		if _, err := rand.Read(s.controlKey[:]); err != nil {
			return [32]byte{}, domain.ErrUnsafe
		}
		s.controlKeySet = true
	}
	// Only safe metadata is JSON-encoded here; raw data is fed directly to an ephemeral HMAC.
	header, err := json.Marshal(struct {
		Selection OutputSelection
		Intent    domain.ControlIntentState
	}{request.Selection, request.Intent.Snapshot()})
	if err != nil {
		return [32]byte{}, domain.ErrInvalid
	}
	digest := hmac.New(sha256.New, s.controlKey[:])
	_, _ = digest.Write(header)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(data)
	var fingerprint [32]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint, nil
}
func validateControlRequest(request ControlRequest) error {
	intent, err := domain.ControlIntentFrom(request.Intent.Snapshot())
	if err != nil {
		return xerrors.Errorf("validate runtime control intent: %w", err)
	}
	state := intent.Snapshot()
	if state.Kind == domain.ControlInput {
		if len(request.Data) != state.InputBytes {
			return domain.ErrInvalid
		}
	} else if len(request.Data) != 0 {
		return domain.ErrInvalid
	}
	return nil
}

// Control makes one explicit bounded operation against an actual current owned
// child. Existing receipts are inspected first and never cause another effect.
func (s *Runtime) Control(ctx context.Context, request ControlRequest) (ControlResult, error) {
	if err := ctx.Err(); err != nil {
		return ControlResult{}, xerrors.Errorf("admit runtime control: %w", err)
	}
	if err := validateControlRequest(request); err != nil {
		return ControlResult{}, err
	}
	s.mu.Lock()
	run, err := s.controlRunLocked(request.Selection)
	if err != nil {
		s.mu.Unlock()
		return ControlResult{}, err
	}
	fingerprint, err := s.controlFingerprintLocked(request, request.Data)
	if err != nil {
		s.mu.Unlock()
		return ControlResult{}, err
	}
	if run.controls != nil {
		if ticket, found := run.controls.tickets[request.Intent.Snapshot().RequestID]; found {
			if ticket.intent.Snapshot() != request.Intent.Snapshot() || !hmac.Equal(ticket.fingerprint[:], fingerprint[:]) {
				s.mu.Unlock()
				return ControlResult{}, domain.ErrChanged
			}
			s.mu.Unlock()
			return s.awaitControl(ctx, ticket)
		}
	}
	if s.revisionExhausted || s.revision == math.MaxUint64 {
		s.mu.Unlock()
		return ControlResult{}, domain.ErrLimit
	}
	if s.shutdownPreparing || (s.shutdownCommitted || s.lifecycleUncertain) && request.Intent.Snapshot().Kind != domain.ControlStop {
		s.mu.Unlock()
		return ControlResult{}, ErrRuntimeBusy
	}
	child, ok := run.child.(ControlledChild)
	if !ok {
		s.mu.Unlock()
		return ControlResult{}, domain.ErrUnsupported
	}
	journal, ok := s.journal.(ControlJournal)
	if !ok {
		s.mu.Unlock()
		return ControlResult{}, domain.ErrUnsupported
	}
	if run.descriptor.Snapshot().State != domain.RunLive {
		s.mu.Unlock()
		return ControlResult{}, domain.ErrChanged
	}
	if s.controlsPending >= domain.MaxControlCoordinators || run.controls != nil && run.controls.busy {
		s.mu.Unlock()
		return ControlResult{}, ErrRuntimeBusy
	}
	if run.controls == nil {
		ledger, e := domain.NewControlLedger(request.Selection.Identity)
		if e != nil {
			s.mu.Unlock()
			return ControlResult{}, xerrors.Errorf("bind runtime control ledger: %w", e)
		}
		run.controls = &runControls{ledger: ledger, tickets: make(map[string]*controlTicket)}
	}
	ticket := &controlTicket{intent: request.Intent, fingerprint: fingerprint, done: make(chan struct{}), result: ControlResult{Selection: request.Selection, Receipt: domain.ControlSnapshot{Identity: request.Selection.Identity, Intent: request.Intent.Snapshot(), Outcome: domain.ControlPending}}}
	run.controls.tickets[request.Intent.Snapshot().RequestID] = ticket
	run.controls.busy = true
	s.controlsPending++
	s.notifyControlLocked()
	data := append([]byte(nil), request.Data...)
	s.mu.Unlock()
	go s.performControl(context.WithoutCancel(ctx), run, ticket, child, journal, data)
	return s.awaitControl(ctx, ticket)
}
func (s *Runtime) awaitControl(ctx context.Context, ticket *controlTicket) (ControlResult, error) {
	select {
	case <-ticket.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return ticket.result, ticket.err
	case <-ctx.Done():
		s.mu.Lock()
		defer s.mu.Unlock()
		if ticket.result.Receipt.Outcome != domain.ControlPending {
			return ticket.result, ticket.err
		}
		view := ticket.result
		view.Receipt.Outcome = domain.ControlUnknown
		return view, ErrControlUnknown
	}
}

// Receipt inspects current-epoch safe evidence even after child exit or changed
// disk witnesses. It never creates a control coordinator or restores authority.
func (s *Runtime) Receipt(selection OutputSelection, id string) (ControlResult, error) {
	if _, err := domain.ControlRequestIDFrom(id); err != nil {
		return ControlResult{}, xerrors.Errorf("validate receipt reference: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.controlRunLocked(selection)
	if err != nil {
		return ControlResult{}, err
	}
	if run.controls == nil {
		return ControlResult{}, domain.ErrNotFound
	}
	ticket, found := run.controls.tickets[id]
	if !found {
		return ControlResult{}, domain.ErrNotFound
	}
	return ticket.result, nil
}
func (s *Runtime) notifyControlLocked() { s.changedLocked() }
func (s *Runtime) finishControl(run *ownedRun, ticket *controlTicket, result ControlResult, err error, drop bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket.result = result
	ticket.err = err
	if drop {
		delete(run.controls.tickets, ticket.intent.Snapshot().RequestID)
	}
	run.controls.busy = false
	s.controlsPending--
	s.notifyControlLocked()
	close(ticket.done)
}
func (s *Runtime) rejectControl(run *ownedRun, ticket *controlTicket, err error) {
	result := ticket.result
	result.Receipt.Outcome = domain.ControlRejected
	s.finishControl(run, ticket, result, err, true)
}
func (s *Runtime) performControl(parent context.Context, run *ownedRun, ticket *controlTicket, child ControlledChild, journal ControlJournal, data []byte) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	// Clear only our owned copy; caller memory and OS/Go transient buffers are not a secure-erasure claim.
	defer clear(data)
	s.mu.Lock()
	snapshot := run.descriptor.Snapshot()
	s.mu.Unlock()
	if err := s.registry.Revalidate(ctx, snapshot.Target, string(snapshot.Profile.ID())); err != nil {
		s.rejectControl(run, ticket, xerrors.Errorf("revalidate control target: %w", err))
		return
	}
	profile, err := s.profiles.LookupProfile(ctx, snapshot.Profile.ID())
	if err != nil {
		s.rejectControl(run, ticket, xerrors.Errorf("revalidate control profile: %w", err))
		return
	}
	if profile != snapshot.Profile {
		s.rejectControl(run, ticket, domain.ErrChanged)
		return
	}
	if err = child.ValidateControl(ctx, ticket.intent); err != nil {
		s.rejectControl(run, ticket, xerrors.Errorf("revalidate native control witness: %w", err))
		return
	}
	if err = ctx.Err(); err != nil {
		s.rejectControl(run, ticket, xerrors.Errorf("validate control admission budget: %w", err))
		return
	}
	s.mu.Lock()
	if run.descriptor.Snapshot().State != domain.RunLive {
		s.mu.Unlock()
		s.rejectControl(run, ticket, domain.ErrChanged)
		return
	}
	pending, _, err := run.controls.ledger.Reserve(ticket.intent, ticket.fingerprint)
	if err == nil {
		ticket.result.Receipt = pending
	}
	s.mu.Unlock()
	if err != nil {
		s.rejectControl(run, ticket, xerrors.Errorf("reserve runtime control receipt: %w", err))
		return
	}
	receipt, err := domain.ControlReceiptFrom(pending)
	if err != nil {
		s.rollbackControl(run, ticket, err)
		return
	}
	err = journal.ReserveControl(ctx, ticket.result.Selection, receipt)
	if err != nil {
		clear(data)
		if !errors.Is(err, ErrControlCommitUncertain) && ctx.Err() == nil {
			s.rollbackControl(run, ticket, xerrors.Errorf("reserve durable control intent: %w", err))
			return
		}
		s.completeControl(parent, run, ticket, journal, ControlEffect{Outcome: domain.ControlUnknown}, ErrControlUnknown)
		return
	}
	effect := ControlEffect{Outcome: domain.ControlUnknown}
	if ctx.Err() == nil {
		effect, err = child.ApplyControl(ctx, ticket.intent, data)
	} else {
		err = ctx.Err()
	}
	clear(data)
	if err != nil {
		effect.Outcome = domain.ControlUnknown
	}
	if effect.Outcome != domain.ControlDelivered && effect.Outcome != domain.ControlUnknown && effect.Outcome != domain.ControlRejected {
		effect.Outcome = domain.ControlUnknown
	}
	candidate := pending
	candidate.Outcome = effect.Outcome
	candidate.WrittenBytes = effect.WrittenBytes
	if _, validationErr := domain.ControlReceiptFrom(candidate); validationErr != nil {
		effect = ControlEffect{Outcome: domain.ControlUnknown}
	}
	s.completeControl(parent, run, ticket, journal, effect, err)
}
func (s *Runtime) rollbackControl(run *ownedRun, ticket *controlTicket, err error) {
	s.mu.Lock()
	abandonErr := run.controls.ledger.AbandonUncommitted(ticket.intent.Snapshot().RequestID)
	s.mu.Unlock()
	if abandonErr != nil {
		result := ticket.result
		result.Receipt.Outcome = domain.ControlUnknown
		s.finishControl(run, ticket, result, ErrControlUnknown, false)
		return
	}
	s.rejectControl(run, ticket, err)
}
func (s *Runtime) completeControl(parent context.Context, run *ownedRun, ticket *controlTicket, journal ControlJournal, effect ControlEffect, effectErr error) {
	s.mu.Lock()
	result := ticket.result
	s.mu.Unlock()
	result.Receipt.Outcome = effect.Outcome
	result.Receipt.WrittenBytes = effect.WrittenBytes
	receipt, err := domain.ControlReceiptFrom(result.Receipt)
	if err != nil {
		result.Receipt.Outcome = domain.ControlUnknown
		result.Receipt.WrittenBytes = 0
		receipt, _ = domain.ControlReceiptFrom(result.Receipt)
	}
	// Bounded post-effect reconciliation retains ownership even if the caller is gone.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	journalErr := journal.SaveControl(saveCtx, result.Selection, receipt)
	cancel()
	if journalErr != nil || effectErr != nil {
		result.Receipt.Outcome = domain.ControlUnknown
	}
	s.mu.Lock()
	final, finalErr := run.controls.ledger.Complete(ticket.intent.Snapshot().RequestID, result.Receipt.Outcome, result.Receipt.WrittenBytes)
	s.mu.Unlock()
	if finalErr == nil {
		result.Receipt = final
	} else {
		result.Receipt.Outcome = domain.ControlUnknown
	}
	var outcomeErr error
	switch result.Receipt.Outcome {
	case domain.ControlDelivered:
	case domain.ControlRejected:
		outcomeErr = ErrRuntimeRejected
	default:
		outcomeErr = ErrControlUnknown
	}
	s.finishControl(run, ticket, result, outcomeErr, false)
}
