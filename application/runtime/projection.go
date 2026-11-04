package runtime

import (
	"context"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ObservationReceipt is an opaque actual-owner intake stamp, never a native
// provenance/continuity proof. It has no wire representation or public fields.
type ObservationReceipt struct {
	owner    *Runtime
	run      *ownedRun
	selected SelectedObservation
	received domain.ProjectionClock
}

func projectionSelection(s OutputSelection) domain.ProjectionSelection {
	return domain.ProjectionSelection{Identity: s.Identity, Worktree: s.Worktree, Profile: s.Profile}
}
func (s *Runtime) observationClockLocked() domain.ProjectionClock {
	now := time.Now()
	return domain.ProjectionClock{Elapsed: now.Sub(s.observationOrigin), Wall: now}
}

// AdmitObservation checks the existing cheap owner predicate and stamps intake
// BEFORE the existing queue. No filesystem, native probes or new queue/worker.
func (s *Runtime) AdmitObservation(ctx context.Context, o SelectedObservation) (ObservationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return ObservationReceipt{}, xerrors.Errorf("admit projection observation: %w", err)
	}
	if err := validateSelectedObservation(o); err != nil {
		return ObservationReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ObservationReceipt{}, xerrors.Errorf("admit projection observation: %w", err)
	}
	run, err := s.observationRunLocked(o)
	if err != nil {
		return ObservationReceipt{}, err
	}
	return ObservationReceipt{owner: s, run: run, selected: o, received: s.observationClockLocked()}, nil
}

func (r *ownedRun) ensureProjection() error {
	if r.projection != nil {
		return nil
	}
	snapshot := r.descriptor.Snapshot()
	var err error
	r.projection, err = domain.NewObservationProjection(domain.ProjectionSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}, snapshot.State)
	if err != nil {
		return xerrors.Errorf("construct owned observation projection: %w", err)
	}
	return nil
}
func (r *ownedRun) recordUnboundProjection(o SelectedObservation, received, now domain.ProjectionClock) {
	if r.ensureProjection() != nil {
		return
	}
	m := o.Metadata.Snapshot()
	e, err := domain.ProjectionEventOf(domain.ProjectionEventState{Selection: projectionSelection(o.Selection), Source: m.Host, SourceVersion: m.HostVersion, Boundary: m.Kind, EventID: m.EventID, NativeSessionID: m.NativeSessionID, TurnID: m.NativeTurnID, Kind: domain.ProjectionUnknown, Received: received})
	if err == nil {
		err = r.projection.ObserveUnbound(e, now)
	}
	if err != nil {
		r.projection.Invalidate(domain.ProjectionGap)
	}
}

// ObservationView is one latest metadata snapshot, not an event stream or native
// authority. Run exposes actual publication uncertainty independently of Process.
type ObservationView struct {
	Run        domain.RunSnapshot
	Projection domain.ProjectionSnapshot
	Changed    <-chan struct{}
}

// SubscribeObservations atomically snapshots and borrows the owner's existing
// change channel, with no lost-wake window, per-reader queue or worker. Unrelated
// owner changes can wake readers; compare full selection and projection Version.
// Skipped versions mean intermediate snapshots/Attention may have been missed.
// There is NO expiry wake promise: reread no later than positive RefreshAfter.
// Native-current admission is intentionally absent; host qualification must first
// guard both commit/read using the SAME original 2464 continuity stamp.
func (s *Runtime) SubscribeObservations(selection OutputSelection) (ObservationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.selectedRunLocked(selection)
	if err != nil {
		return ObservationView{}, err
	}
	if err = run.ensureProjection(); err != nil {
		return ObservationView{}, err
	}
	snapshot := run.descriptor.Snapshot()
	if err = run.projection.UpdateProcess(snapshot.State); err != nil {
		return ObservationView{}, xerrors.Errorf("refresh projected process state: %w", err)
	}
	return ObservationView{Run: snapshot, Projection: run.projection.Snapshot(s.observationClockLocked()), Changed: s.changed}, nil
}
