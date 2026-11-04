package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ResumeRequest is an explicit loss-acknowledged operator action. It contains no
// native ID, input, tool, approval, executable, environment or target override.
type ResumeRequest struct {
	Epoch                string
	Selection            OutputSelection
	RequestID            string
	WorkLossAcknowledged bool
}

// Valid checks syntax and explicit consent, not source/exit/ownership authority.
func (r ResumeRequest) Valid() bool {
	if !r.WorkLossAcknowledged || !r.Selection.Identity.Valid() || !(domain.LaunchIdentity{Run: r.Epoch, Generation: r.Epoch, Epoch: r.Epoch}).Valid() {
		return false
	}
	if !validLaunchRequest(LaunchRequest{Epoch: r.Epoch, Worktree: r.Selection.Worktree, Profile: r.Selection.Profile, RequestID: r.RequestID}) {
		return false
	}
	worktree, err := hex.DecodeString(string(r.Selection.Worktree)[2:])
	if err != nil || hex.EncodeToString(worktree) != string(r.Selection.Worktree)[2:] {
		return false
	}
	_, err = domain.ControlRequestIDFrom(r.RequestID)
	return err == nil
}

// ResumeClient sends one explicit management operation, never an implicit retry.
type ResumeClient interface {
	Resume(context.Context, string, ResumeRequest) (ResumeResult, error)
}

// ResumeResult separates recorded outcome from actual in-memory ownership.
// Owned means this Runtime holds the actual child handle, not that it is live.
// A stored result after restart never reconstructs ownership or control rights.
type ResumeResult struct {
	Selection OutputSelection
	Phase     domain.ResumePhase
	Owned     bool
}

// RecoveryJournal is trusted bookkeeping, never a public write API.
type RecoveryJournal interface {
	RecoveryRecorder
	ListRecovery(context.Context) ([]domain.RecoveryRecordState, error)
	ListResumeAttempts(context.Context) ([]domain.ResumeAttemptState, error)
	ReserveResume(context.Context, domain.ResumeAttemptState) (domain.ResumeAttemptState, bool, error)
	SaveResume(context.Context, domain.ResumeAttemptState, domain.ResumeAttemptState) error
}

// NativeResumer directly owns the ordinary qualified CLI resume operation.
// RevalidateRecovery must verify the original native source/target/model and
// no-replay contract. Resume transfers the actual child once Start may commit;
// an error without a child must be ErrLaunchUnknown if creation is ambiguous.
// No wrappers, app-server mode, model API or automatic pending-input replay.
type NativeResumer interface {
	RevalidateRecovery(context.Context, domain.RecoveryBinding) error
	Resume(context.Context, domain.LaunchDescriptor, domain.RecoveryBinding) (OwnedChild, error)
}

type resumeTicket struct {
	request                                     ResumeRequest
	ctx                                         context.Context
	cancel                                      context.CancelFunc
	done                                        chan struct{}
	waiters                                     int
	starting, complete, attached, possibleChild bool
	descriptor                                  domain.LaunchDescriptor
	result                                      ResumeResult
	err                                         error
}

// Resume serializes explicit requests. A loaded reservation is inspect-only;
// only this operation's newly committed intent may proceed toward one Start.
func (s *Runtime) Resume(ctx context.Context, request ResumeRequest) (ResumeResult, error) {
	if err := ctx.Err(); err != nil {
		return ResumeResult{}, xerrors.Errorf("admit managed resume: %w", err)
	}
	if request.Epoch != s.epoch {
		return ResumeResult{}, domain.ErrChanged
	}
	if !request.Valid() {
		return ResumeResult{}, domain.ErrInvalid
	}
	journal, ok := s.journal.(RecoveryJournal)
	if !ok {
		return ResumeResult{}, domain.ErrUnsupported
	}
	s.mu.Lock()
	ticket, exists := s.resumes[request.RequestID]
	if exists && ticket.request != request {
		s.mu.Unlock()
		return ResumeResult{}, domain.ErrChanged
	}
	if !exists {
		if s.disabled {
			s.mu.Unlock()
			return ResumeResult{}, ErrRuntimeBusy
		}
		if s.pending+s.active >= domain.MaxActiveRuns || len(s.tickets)+len(s.resumes) >= domain.MaxLaunchRequests {
			s.mu.Unlock()
			return ResumeResult{}, domain.ErrLimit
		}
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		ticket = &resumeTicket{request: request, ctx: work, cancel: cancel, done: make(chan struct{})}
		s.resumes[request.RequestID] = ticket
		s.pending++
		s.changedLocked()
	}
	ticket.waiters++
	s.mu.Unlock()
	if !exists {
		go s.resume(ticket, journal)
	}
	select {
	case <-ticket.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		ticket.waiters--
		return ticket.result, ticket.err
	case <-ctx.Done():
		s.mu.Lock()
		defer s.mu.Unlock()
		ticket.waiters--
		if ticket.complete {
			return ticket.result, ticket.err
		}
		if !ticket.starting {
			if ticket.waiters == 0 {
				ticket.cancel()
			}
			return ResumeResult{}, xerrors.Errorf("wait for managed resume: %w", ctx.Err())
		}
		return ticket.result, ErrLaunchUnknown
	}
}

func resumeSelection(binding domain.RecoveryBinding, id domain.LaunchIdentity) OutputSelection {
	b := binding.Snapshot()
	return OutputSelection{Identity: id, Worktree: b.Target.Worktree().ID(), Profile: b.Profile.ID()}
}
func (s *Runtime) completeResume(t *resumeTicket, result ResumeResult, err error, possibleChild bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.result, t.err, t.possibleChild = result, err, possibleChild
	if !t.attached && !possibleChild {
		s.pending--
	}
	t.complete = true
	s.changedLocked()
	close(t.done)
}
func storedResumeResult(state domain.ResumeAttemptState) (ResumeResult, error) {
	result := ResumeResult{Selection: resumeSelection(state.Binding, state.Next), Phase: state.Phase}
	switch state.Phase {
	case domain.ResumeConfirmed:
		return result, nil
	case domain.ResumeFailedBeforeStart:
		return result, ErrRuntimeRejected
	default:
		return result, ErrLaunchUnknown
	}
}
func resumeMatchesRequest(state domain.ResumeAttemptState, request ResumeRequest) bool {
	return state.RequestID == request.RequestID && resumeSelection(state.Binding, state.Binding.Snapshot().Identity) == request.Selection
}
func (s *Runtime) resume(t *resumeTicket, journal RecoveryJournal) {
	defer t.cancel()
	// Inspect existing outcomes before current host availability/preflight. No
	// stored reserved/started/unknown phase is permission to call Resume again.
	attempts, err := journal.ListResumeAttempts(t.ctx)
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	for _, state := range attempts {
		if state.RequestID != t.request.RequestID {
			continue
		}
		if !resumeMatchesRequest(state, t.request) {
			s.completeResume(t, ResumeResult{}, domain.ErrChanged, false)
			return
		}
		result, err := storedResumeResult(state)
		s.completeResume(t, result, err, false)
		return
	}
	native, ok := s.launcher.(NativeResumer)
	if !ok {
		s.completeResume(t, ResumeResult{}, domain.ErrUnsupported, false)
		return
	}
	records, err := journal.ListRecovery(t.ctx)
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	var selected *domain.RecoveryRecord
	for _, state := range records {
		if resumeSelection(state.Binding, state.Binding.Snapshot().Identity) != t.request.Selection {
			continue
		}
		if selected != nil {
			s.completeResume(t, ResumeResult{}, domain.ErrInvalid, false)
			return
		}
		selected, err = domain.RecoveryRecordFrom(state)
		if err != nil {
			s.completeResume(t, ResumeResult{}, err, false)
			return
		}
	}
	if selected == nil || !selected.Snapshot().ExitObserved {
		s.completeResume(t, ResumeResult{}, domain.ErrChanged, false)
		return
	}
	binding := selected.Snapshot().Binding
	b := binding.Snapshot()
	s.mu.Lock()
	old := s.runs[b.Identity.Run]
	contradicts := old != nil && (old.descriptor.Snapshot().Identity != b.Identity || old.descriptor.Snapshot().State != domain.RunExited || old.waitErr != nil)
	s.mu.Unlock()
	if contradicts {
		s.completeResume(t, ResumeResult{}, domain.ErrChanged, false)
		return
	}
	target, err := s.registry.Resolve(t.ctx, t.request.Selection.Worktree, string(t.request.Selection.Profile))
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	profile, err := s.profiles.LookupProfile(t.ctx, t.request.Selection.Profile)
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	if target != b.Target || profile != b.Profile {
		s.completeResume(t, ResumeResult{}, domain.ErrChanged, false)
		return
	}
	if err = s.registry.Revalidate(t.ctx, target, string(profile.ID())); err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	if err = native.RevalidateRecovery(t.ctx, binding); err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	id, err := newLaunchIdentity(s.epoch)
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	descriptor, err := domain.LaunchDescriptorOf(id, target, profile)
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	attempt, err := domain.NewResumeAttempt(t.request.RequestID, selected, id)
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	result := ResumeResult{Selection: resumeSelection(binding, id), Phase: domain.ResumeReserved}
	s.mu.Lock()
	err = t.ctx.Err()
	if err == nil && s.disabled {
		err = ErrRuntimeBusy
	}
	if err == nil {
		t.starting = true
		t.descriptor = descriptor
		t.result = ResumeResult{Phase: domain.ResumeUnknown}
		s.changedLocked()
	}
	s.mu.Unlock()
	if err != nil {
		s.completeResume(t, ResumeResult{}, err, false)
		return
	}
	saved, created, err := journal.ReserveResume(t.ctx, attempt.Snapshot())
	if err != nil {
		result.Phase = domain.ResumeUnknown
		result.Selection = OutputSelection{}
		if !errors.Is(err, domain.ErrChanged) && !errors.Is(err, domain.ErrInvalid) && !errors.Is(err, domain.ErrLimit) && !errors.Is(err, domain.ErrUnsupported) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = ErrLaunchUnknown
		}
		s.completeResume(t, result, err, false)
		return // Start was NOT called.
	}
	if !created {
		if !resumeMatchesRequest(saved, t.request) {
			s.completeResume(t, result, domain.ErrChanged, false)
			return
		}
		result, err = storedResumeResult(saved)
		s.completeResume(t, result, err, false)
		return
	}
	s.mu.Lock()
	t.result = result
	s.changedLocked()
	s.mu.Unlock()
	if err = s.journal.ReserveLaunch(t.ctx, descriptor.Snapshot()); err != nil {
		s.resumeBeforeStartFailure(t, journal, attempt, result, err)
		return
	}
	if err = t.ctx.Err(); err != nil {
		s.resumeBeforeStartFailure(t, journal, attempt, result, err)
		return
	}
	child, startErr := native.Resume(t.ctx, descriptor, binding)
	if child == nil {
		if startErr == nil || errors.Is(startErr, ErrLaunchUnknown) {
			s.recordResumeUnknown(journal, attempt)
			result.Phase = domain.ResumeUnknown
			s.completeResume(t, result, ErrLaunchUnknown, true)
			return
		}
		s.resumeBeforeStartFailure(t, journal, attempt, result, startErr)
		return
	}
	// Retain every actual transferred child, even if the adapter violated its
	// contract by returning it alongside an error. Never automatically kill it.
	descriptorErr := descriptor.Started(child.PID())
	if descriptorErr != nil {
		_ = descriptor.MarkUncertain()
	}
	run := &ownedRun{descriptor: descriptor, child: child, done: make(chan struct{}), recoveryExpected: binding}
	result.Owned = true
	s.mu.Lock()
	s.pending--
	s.active++
	s.runs[id.Run] = run
	t.attached = true
	t.result = result
	t.descriptor = descriptor
	s.changedLocked()
	s.mu.Unlock()
	persist, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	saveErr := s.journal.SaveLaunch(persist, descriptor.Snapshot())
	cancel()
	if startErr == nil && descriptorErr == nil && saveErr == nil {
		before := attempt.Snapshot()
		saveErr = attempt.MarkStarted()
		if saveErr == nil {
			persist, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			saveErr = journal.SaveResume(persist, before, attempt.Snapshot())
			cancel()
		}
	}
	if startErr == nil && descriptorErr == nil && saveErr == nil {
		s.mu.Lock()
		t.result.Phase = domain.ResumeStarted
		s.changedLocked()
		s.mu.Unlock()
		saveErr = s.confirmResume(journal, attempt, child)
	}
	s.mu.Lock()
	if startErr != nil || descriptorErr != nil || saveErr != nil {
		run.markUncertain()
	} else {
		run.published = true
	}
	s.changedLocked()
	s.mu.Unlock()
	if startErr != nil || descriptorErr != nil || saveErr != nil {
		s.recordResumeUnknown(journal, attempt)
		result.Phase = domain.ResumeUnknown
		err = ErrLaunchUnknown
	} else {
		result.Phase = domain.ResumeConfirmed
		err = nil
	}
	s.completeResume(t, result, err, false)
	go s.reconcile(run)
}
func (s *Runtime) resumeBeforeStartFailure(t *resumeTicket, journal RecoveryJournal, attempt *domain.ResumeAttempt, result ResumeResult, cause error) {
	before := attempt.Snapshot()
	err := attempt.FailBeforeStart()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = journal.SaveResume(ctx, before, attempt.Snapshot())
		cancel()
	}
	if err != nil {
		result.Phase = domain.ResumeUnknown
		cause = ErrLaunchUnknown
	} else {
		result.Phase = domain.ResumeFailedBeforeStart
	}
	s.completeResume(t, result, cause, false)
}
func (s *Runtime) recordResumeUnknown(journal RecoveryJournal, attempt *domain.ResumeAttempt) {
	before := attempt.Snapshot()
	if attempt.MarkUnknown() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = journal.SaveResume(ctx, before, attempt.Snapshot())
}
func (s *Runtime) confirmResume(journal RecoveryJournal, attempt *domain.ResumeAttempt, child OwnedChild) error {
	source, ok := child.(RecoveryBindingSource)
	if !ok {
		return domain.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	binding, err := source.RecoveryBinding(ctx)
	if err != nil {
		return xerrors.Errorf("verify resumed native binding: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return xerrors.Errorf("confirm native recovery budget: %w", err)
	}
	before := attempt.Snapshot()
	candidate, err := domain.ResumeAttemptFrom(before)
	if err != nil {
		return xerrors.Errorf("check recovery confirmation: %w", err)
	}
	if err = candidate.Confirm(binding); err != nil {
		return xerrors.Errorf("confirm same native conversation: %w", err)
	}
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		return xerrors.Errorf("bind resumed native owner: %w", err)
	}
	if err = journal.SaveRecovery(ctx, record.Snapshot()); err != nil {
		return xerrors.Errorf("persist resumed native binding: %w", err)
	}
	if err = journal.SaveResume(ctx, before, candidate.Snapshot()); err != nil {
		return xerrors.Errorf("persist confirmed resume outcome: %w", err)
	}
	if err = attempt.Confirm(binding); err != nil {
		return xerrors.Errorf("retain confirmed recovery result: %w", err)
	}
	return nil
}

// SupportsResume advertises only adapter availability, not any profile/version
// qualification. Individual source/target/native checks remain mandatory.
func (s *Runtime) SupportsResume() bool {
	_, journal := s.journal.(RecoveryJournal)
	_, native := s.launcher.(NativeResumer)
	return journal && native
}
