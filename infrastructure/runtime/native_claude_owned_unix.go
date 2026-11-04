//go:build darwin || linux

package runtime

import (
	"context"
	"sync"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ownedClaudeRecovery is private metadata lifetime attached to the actual PTY
// child, not an agent wrapper, independent process owner or saved-PID adapter.
// The launcher must assign it before starting the child's Wait goroutine.
type ownedClaudeRecovery struct {
	slot        *nativeStartSlot
	environment []string
	mu          sync.Mutex
	finished    bool
	final       nativeStartEvent
	finalErr    error
}

func (s *ownedClaudeRecovery) finish() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.final, s.finalErr = s.slot.finish(ctx)
	s.finished = true
}

func (s *ownedClaudeRecovery) event(ctx context.Context) (nativeStartEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return s.final, s.finalErr
	}
	return s.slot.read(ctx)
}

// RecoveryBinding does not establish exit: Runtime still requires the actual
// child's successful Wait plus a durable exact execution receipt separately.
func (c *ptyChild) RecoveryBinding(ctx context.Context) (domain.RecoveryBinding, error) {
	s := c.nativeRecovery
	if s == nil {
		return domain.RecoveryBinding{}, domain.ErrUnsupported
	}
	event, err := s.event(ctx)
	if err != nil {
		return domain.RecoveryBinding{}, err
	}
	if event.Plan.Identity != c.selection.Identity || event.Plan.CWD != c.target.Worktree().Path() ||
		event.Plan.Store != c.target.Worktree().Store() || c.pid <= 0 {
		return domain.RecoveryBinding{}, domain.ErrChanged
	}
	source, err := inspectClaudeRecoverySource(ctx, c.profile, s.environment)
	if err != nil {
		return domain.RecoveryBinding{}, err
	}
	if source.Namespace != event.Plan.Namespace {
		return domain.RecoveryBinding{}, domain.ErrChanged
	}
	history, err := readClaudeRecoveryHistory(ctx, event)
	if err != nil {
		return domain.RecoveryBinding{}, err
	}
	if err := verifyClaudeRecoveryProvider(ctx, source, s.environment, event.Plan.CWD); err != nil {
		return domain.RecoveryBinding{}, err
	}
	// Recheck local source and latest callback after the potentially slow native
	// status operation. Never promote an earlier cached live binding at exit.
	again, err := inspectClaudeRecoverySource(ctx, c.profile, s.environment)
	if err != nil {
		return domain.RecoveryBinding{}, err
	}
	latest, err := s.event(ctx)
	if err != nil {
		return domain.RecoveryBinding{}, err
	}
	if source != again || latest != event {
		return domain.RecoveryBinding{}, domain.ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return domain.RecoveryBinding{}, domain.ErrChanged
	}
	binding, err := domain.RecoveryBindingOf(domain.RecoveryBindingState{
		Identity: c.selection.Identity, Target: c.target, Profile: c.profile, PID: c.pid,
		NativeSessionID: event.Plan.SessionID, NativeHistoryPath: event.TranscriptPath,
		NativeNamespaceWitness: history.NamespaceWitness, Model: history.Model, Provider: "claude.ai:firstParty",
	})
	if err != nil {
		return domain.RecoveryBinding{}, xerrors.Errorf("validate owned native binding: %w", err)
	}
	return binding, nil
}
