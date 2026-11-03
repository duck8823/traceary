package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	"golang.org/x/xerrors"
)

// LifecycleClock is trusted lifecycle-notice composition, NOT a client-supplied
// timestamp or a clock for native interaction freshness/authorization. Now must
// be bounded/nonblocking; it is invoked outside the process-ownership mutex.
type LifecycleClock interface{ Now() time.Time }
type runtimeWallClock struct{}

func (runtimeWallClock) Now() time.Time { return time.Now() }

// NewRuntimeWithClock keeps existing NewRuntime callers unchanged while enabling
// deterministic lifecycle scheduling. The clock never confers native authority.
func NewRuntimeWithClock(registry TargetRegistry, profiles ProfileCatalog, launcher ProcessLauncher, journal LaunchJournal, epoch string, clock LifecycleClock) (*Runtime, error) {
	if clock == nil {
		return nil, domain.ErrInvalid
	}
	owner, err := NewRuntime(registry, profiles, launcher, journal, epoch)
	if err != nil {
		return nil, err
	}
	owner.clock = clock
	return owner, nil
}

// UpdateStatus is a read-only eligibility snapshot, never updater permission,
// successful installation, native-session exclusivity or proof of human notice.
type UpdateStatus struct {
	Epoch      string
	Quiescent  bool
	Blockers   []string
	Deferral   types.Optional[domain.UpdateDeferralState]
	Overdue24h bool
}

// UpdateAttemptResult reports a valid supported attempt, including busy or
// persistence failures. Target is request correlation; the returned error still
// governs acceptance. Only successful quiescent results include an exact empty
// shutdown preview, which neither freezes admission nor executes an updater.
type UpdateAttemptResult struct {
	Target  domain.UpdateTarget
	Status  UpdateStatus
	Notice  bool
	Preview types.Optional[domain.ShutdownBindingState]
}

func (s *Runtime) updateStatusLocked(now time.Time) UpdateStatus {
	binding := s.shutdownBindingLocked("")
	blockers := make([]string, 0, 8)
	if binding.TotalRuns != 0 {
		blockers = append(blockers, "active_runs")
	}
	if binding.PendingLaunches != 0 {
		blockers = append(blockers, "pending_launches")
	}
	if binding.PendingControls != 0 {
		blockers = append(blockers, "pending_controls")
	}
	if binding.PendingReconciliations != 0 {
		blockers = append(blockers, "pending_reconciliations")
	}
	if s.lifecycleStarting || s.lifecyclePending != 0 {
		blockers = append(blockers, "lifecycle_busy")
	}
	if s.hasUncertainLocked() {
		blockers = append(blockers, "publication_uncertain")
	}
	if s.disabled || s.shutdownPreparing || s.shutdownCommitted {
		blockers = append(blockers, "admission_closed")
	}
	status := UpdateStatus{Epoch: s.epoch, Quiescent: len(blockers) == 0, Blockers: blockers}
	if s.updateDeferral != nil {
		status.Deferral = types.Some(s.updateDeferral.Snapshot())
		status.Overdue24h = s.updateDeferral.Overdue24h(now)
	}
	return status
}

func (s *Runtime) updateRepositoryLocked() (LifecycleRepository, error) {
	repository, ok := s.journal.(LifecycleRepository)
	if !ok || !s.lifecycleBound {
		return nil, domain.ErrUnsupported
	}
	return repository, nil
}
func (s *Runtime) updateMetadataBusyLocked() bool {
	return s.disabled || s.lifecycleStarting || s.lifecyclePending != 0 || s.shutdownPreparing || s.shutdownCommitted || s.lifecycleUncertain
}

// UpdateStatus never consumes a notice or writes metadata, including after24h.
func (s *Runtime) UpdateStatus(ctx context.Context) (UpdateStatus, error) {
	if err := ctx.Err(); err != nil {
		return UpdateStatus{}, xerrors.Errorf("read update status: %w", err)
	}
	now := s.clock.Now().UTC().Round(0)
	if now.IsZero() {
		return UpdateStatus{}, domain.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.updateRepositoryLocked(); err != nil {
		return UpdateStatus{}, err
	}
	return s.updateStatusLocked(now), nil
}

func (s *Runtime) emptyUpdatePreviewLocked(id string) domain.ShutdownBindingState {
	preview := ShutdownPreview{Binding: s.shutdownBindingLocked(id), Runs: []domain.RunSnapshot{}}
	s.shutdownPreview = &preview
	return preview.Binding
}

// AttemptUpdate only records deferral/notice metadata and an optional exact
// empty handoff preview. Active work keeps running and new launches/controls are
// not disabled merely because an update is deferred. No updater is executed.
func (s *Runtime) AttemptUpdate(ctx context.Context, target domain.UpdateTarget) (UpdateAttemptResult, error) {
	if err := ctx.Err(); err != nil {
		return UpdateAttemptResult{}, xerrors.Errorf("attempt daemon update: %w", err)
	}
	now := s.clock.Now().UTC().Round(0)
	candidate, err := domain.NewUpdateDeferral(target, now)
	if err != nil {
		return UpdateAttemptResult{}, xerrors.Errorf("validate update target and clock: %w", err)
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return UpdateAttemptResult{}, domain.ErrUnsafe
	}
	id := hex.EncodeToString(nonce[:])
	s.mu.Lock()
	repository, err := s.updateRepositoryLocked()
	if err != nil {
		s.mu.Unlock()
		return UpdateAttemptResult{}, err
	}
	result := UpdateAttemptResult{Target: target, Status: s.updateStatusLocked(now), Notice: candidate.Notice(now, domain.UpdateAttempt)}
	if s.updateMetadataBusyLocked() {
		s.mu.Unlock()
		return result, ErrRuntimeBusy
	}
	if s.updateDeferral == nil && result.Status.Quiescent {
		result.Preview = types.Some(s.emptyUpdatePreviewLocked(id))
		s.mu.Unlock()
		return result, nil
	}
	if s.updateDeferral != nil {
		candidate, err = domain.UpdateDeferralFrom(s.updateDeferral.Snapshot())
		if err == nil {
			err = candidate.Retarget(target)
		}
		if err != nil {
			s.mu.Unlock()
			return result, xerrors.Errorf("retarget continuous update deferral: %w", err)
		}
	}
	// A due publication is durable metadata, not proven display/OS delivery.
	_ = candidate.Notice(now, domain.UpdateStatus)
	s.lifecyclePending++
	s.changedLocked()
	s.mu.Unlock()
	err = s.persistUpdateDeferral(ctx, repository, candidate)
	s.mu.Lock()
	s.finishUpdateLocked(candidate, err)
	result.Status = s.updateStatusLocked(now)
	if err == nil && result.Status.Quiescent {
		result.Preview = types.Some(s.emptyUpdatePreviewLocked(id))
	}
	s.mu.Unlock()
	return result, err
}

// persistUpdateDeferral performs I/O for an already-admitted coordinator. Its
// caller must finish under the ownership lock even on error. Cancellation does
// not abandon it: AwaitIdle retains ownership until the bounded write is joined.
func (s *Runtime) persistUpdateDeferral(parent context.Context, repository LifecycleRepository, candidate *domain.UpdateDeferral) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 8*time.Second)
	err := repository.SaveUpdateDeferral(ctx, s.epoch, candidate.Snapshot())
	uncertain := err != nil && (errors.Is(err, ErrLifecycleCommitUncertain) || ctx.Err() != nil)
	cancel()
	if err != nil {
		if uncertain {
			err = errors.Join(ErrLifecycleCommitUncertain, err)
		}
		return xerrors.Errorf("persist continuous update deferral: %w", err)
	}
	return nil
}

// Finish and the attempt's response snapshot share ONE lock acquisition: another
// accepted retarget must not replace this attempt's target between counter join
// and the returned empty preview. This is aggregate state, not journal I/O.
func (s *Runtime) finishUpdateLocked(candidate *domain.UpdateDeferral, err error) {
	if err == nil {
		s.updateDeferral = candidate
	} else if errors.Is(err, ErrLifecycleCommitUncertain) {
		s.lifecycleUncertain = true
		s.disabled = true
	}
	s.lifecyclePending--
	s.changedLocked()
}

// PublishDueUpdateNotice publishes threshold metadata once. Repeated status
// reads never call this method. A separate daemon scheduler may retry definite
// failures, but must not claim human delivery or clear continuous start history.
func (s *Runtime) PublishDueUpdateNotice(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("publish update notice: %w", err)
	}
	now := s.clock.Now().UTC().Round(0)
	if now.IsZero() {
		return domain.ErrInvalid
	}
	s.mu.Lock()
	repository, err := s.updateRepositoryLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if s.updateDeferral == nil || s.updateDeferral.Snapshot().Notified24h || !s.updateDeferral.Overdue24h(now) {
		s.mu.Unlock()
		return nil
	}
	if s.updateMetadataBusyLocked() {
		s.mu.Unlock()
		return ErrRuntimeBusy
	}
	candidate, err := domain.UpdateDeferralFrom(s.updateDeferral.Snapshot())
	if err != nil {
		s.mu.Unlock()
		return xerrors.Errorf("restore continuous update notice: %w", err)
	}
	_ = candidate.Notice(now, domain.UpdateStatus)
	s.lifecyclePending++
	s.changedLocked()
	s.mu.Unlock()
	err = s.persistUpdateDeferral(ctx, repository, candidate)
	s.mu.Lock()
	s.finishUpdateLocked(candidate, err)
	s.mu.Unlock()
	return err
}
