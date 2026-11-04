//go:build darwin || linux

package runtime

import (
	"context"
	"math"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// updateRecovery shares the existing private root lock and atomic writer. A
// post-rename error remains uncertain; callers must never infer permission to
// start/retry a native process from any failed journal operation.
func (s *ExecutionStore) updateRecovery(ctx context.Context, mutate func(*recoveryEnvelope, executionEnvelope) (bool, error)) error {
	return s.metadata.locked(ctx, func(fd int) error {
		execution, _, err := s.read(fd)
		if err != nil {
			return err
		}
		doc, stamp, err := s.readRecovery(fd, execution)
		if err != nil {
			return err
		}
		changed, err := mutate(&doc, execution)
		if err != nil {
			return err
		}
		if !changed {
			return s.metadata.verifyRoot(fd)
		}
		if *doc.Revision == math.MaxUint64 {
			return domain.ErrLimit
		}
		*doc.Revision++
		data, err := encodeRecovery(doc, execution)
		if err != nil {
			return err
		}
		return s.metadata.writeMetadata(ctx, fd, "recovery.json", data, stamp)
	})
}

// ListRecovery reads historical bookkeeping only, never a process owner or
// authorization to resume. Native identity data stays on this trusted port.
func (s *ExecutionStore) ListRecovery(ctx context.Context) ([]domain.RecoveryRecordState, error) {
	result := []domain.RecoveryRecordState{}
	err := s.metadata.locked(ctx, func(fd int) error {
		execution, _, err := s.read(fd)
		if err != nil {
			return err
		}
		doc, _, err := s.readRecovery(fd, execution)
		if err != nil {
			return err
		}
		for _, row := range doc.Records {
			record, err := row.domainRecord()
			if err != nil {
				return err
			}
			result = append(result, record.Snapshot())
		}
		return s.metadata.verifyRoot(fd)
	})
	if err != nil {
		return nil, xerrors.Errorf("list recovery bookkeeping: %w", err)
	}
	return result, nil
}

// SaveRecovery is an application-owned journal operation. Its caller supplies
// only a qualified binding and actual owned-Wait receipt, never client flags.
// The binding is immutable and positive exit bookkeeping cannot regress.
func (s *ExecutionStore) SaveRecovery(ctx context.Context, state domain.RecoveryRecordState) error {
	if _, err := domain.RecoveryRecordFrom(state); err != nil {
		return xerrors.Errorf("save recovery bookkeeping: %w", err)
	}
	return s.updateRecovery(ctx, func(doc *recoveryEnvelope, _ executionEnvelope) (bool, error) {
		for i, row := range doc.Records {
			old, err := row.domainRecord()
			if err != nil {
				return false, err
			}
			previous := old.Snapshot()
			if previous.Binding.Snapshot().Identity.Run != state.Binding.Snapshot().Identity.Run {
				continue
			}
			if previous == state {
				return false, nil
			}
			if previous.Binding != state.Binding || previous.ExitObserved {
				return false, domain.ErrChanged
			}
			if !state.ExitObserved {
				return false, domain.ErrChanged
			}
			if err := old.ObserveOwnedExit(state.Binding.Snapshot().Identity, state.ExitCode); err != nil {
				return false, xerrors.Errorf("advance recovery exit receipt: %w", err)
			}
			doc.Records[i] = recordRecovery(old.Snapshot())
			return true, nil
		}
		if len(doc.Records) >= domain.MaxLaunchRequests {
			return false, domain.ErrLimit
		}
		doc.Records = append(doc.Records, recordRecovery(state))
		return true, nil
	})
}

// ReserveResume binds a request to its first successor exactly once. created is
// true only after this operation durably wrote a NEW reservation. Existing
// metadata (even reserved) returns false and never grants restart authority.
// Only the still-owning Runtime operation may combine a new reservation with
// its live in-memory ticket and fresh source checks to proceed toward Start.
func (s *ExecutionStore) ReserveResume(ctx context.Context, proposal domain.ResumeAttemptState) (domain.ResumeAttemptState, bool, error) {
	if _, err := domain.ResumeAttemptFrom(proposal); err != nil {
		return domain.ResumeAttemptState{}, false, xerrors.Errorf("reserve recovery request: %w", err)
	}
	if proposal.Phase != domain.ResumeReserved {
		return domain.ResumeAttemptState{}, false, domain.ErrInvalid
	}
	var saved domain.ResumeAttemptState
	created := false
	err := s.updateRecovery(ctx, func(doc *recoveryEnvelope, execution executionEnvelope) (bool, error) {
		for _, row := range doc.Attempts {
			attempt, err := row.domainAttempt()
			if err != nil {
				return false, err
			}
			existing := attempt.Snapshot()
			if existing.RequestID != proposal.RequestID {
				continue
			}
			if existing.Binding != proposal.Binding || existing.PredecessorExitCode != proposal.PredecessorExitCode {
				return false, domain.ErrChanged
			}
			saved = existing
			return false, nil
		}
		for _, run := range execution.Launches {
			if run.Identity.Run == proposal.Next.Run || run.Identity.Generation == proposal.Next.Generation {
				return false, domain.ErrChanged
			}
		}
		if len(doc.Attempts) >= domain.MaxLaunchRequests {
			return false, domain.ErrLimit
		}
		for _, row := range doc.Attempts {
			attempt, err := row.domainAttempt()
			if err != nil {
				return false, err
			}
			existing := attempt.Snapshot()
			if existing.Phase == domain.ResumeFailedBeforeStart {
				continue
			}
			active := existing.Phase != domain.ResumeConfirmed
			if existing.Phase == domain.ResumeConfirmed {
				for _, run := range execution.Launches {
					if run.Identity.Run == existing.Next.Run {
						active = run.State != string(domain.RunExited)
						break
					}
				}
			}
			if existing.Binding.Snapshot().Identity.Run == proposal.Binding.Snapshot().Identity.Run || active && existing.Binding.ConversationKey() == proposal.Binding.ConversationKey() {
				return false, domain.ErrChanged
			}
		}
		// Cross-envelope and active-conversation validation below also handles a
		// confirmed successor still live. A proposal never adopts an existing run.
		doc.Attempts = append(doc.Attempts, recordResume(proposal))
		saved = proposal
		created = true
		return true, nil
	})
	if err != nil {
		return domain.ResumeAttemptState{}, false, xerrors.Errorf("reserve recovery request: %w", err)
	}
	return saved, created, nil
}

// SaveResume is compare-and-save on a monotonic domain transition. Reloading a
// reserved/unknown attempt is inspect-only: callers need the SAME actual live
// Runtime ticket/child to supply an observed result, never a new Start.
func (s *ExecutionStore) SaveResume(ctx context.Context, before, after domain.ResumeAttemptState) error {
	attempt, err := domain.ResumeAttemptFrom(before)
	if err != nil {
		return xerrors.Errorf("save recovery result: %w", err)
	}
	if _, err := domain.ResumeAttemptFrom(after); err != nil {
		return xerrors.Errorf("save recovery result: %w", err)
	}
	switch after.Phase {
	case domain.ResumeStarted:
		err = attempt.MarkStarted()
	case domain.ResumeUnknown:
		err = attempt.MarkUnknown()
	case domain.ResumeFailedBeforeStart:
		err = attempt.FailBeforeStart()
	case domain.ResumeConfirmed:
		err = attempt.Confirm(after.Confirmed)
	case domain.ResumeReserved:
		if before != after {
			err = domain.ErrChanged
		}
	default:
		return domain.ErrInvalid
	}
	if err != nil {
		return xerrors.Errorf("advance recovery result: %w", err)
	}
	if attempt.Snapshot() != after {
		return domain.ErrChanged
	}
	return s.updateRecovery(ctx, func(doc *recoveryEnvelope, _ executionEnvelope) (bool, error) {
		for i, row := range doc.Attempts {
			current, err := row.domainAttempt()
			if err != nil {
				return false, err
			}
			state := current.Snapshot()
			if state.RequestID != before.RequestID {
				continue
			}
			if state == after {
				return false, nil
			}
			if state != before {
				return false, domain.ErrChanged
			}
			doc.Attempts[i] = recordResume(after)
			return true, nil
		}
		return false, domain.ErrNotFound
	})
}
