package runtime

import (
	"context"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// SelectedObservation joins explicit correlation to an existing immutable owned launch.
// Matching selection alone is never proof of native provenance or readiness.
type SelectedObservation struct {
	Selection OutputSelection
	Metadata  domain.DerivedObservation
}

// SelectedObservationOf validates explicit correlation without reading files or granting native authority.
func SelectedObservationOf(selection OutputSelection, metadata domain.DerivedObservation) (SelectedObservation, error) {
	value := SelectedObservation{Selection: selection, Metadata: metadata}
	if err := validateSelectedObservation(value); err != nil {
		return SelectedObservation{}, err
	}
	return value, nil
}

func validateSelectedObservation(o SelectedObservation) error {
	if _, err := domain.DerivedObservationOf(o.Metadata.Snapshot()); err != nil {
		return xerrors.Errorf("validate selected observation metadata: %w", err)
	}
	if !projectionSelection(o.Selection).Valid() {
		return domain.ErrInvalid
	}
	return nil
}

// observationRunLocked borrows only immutable owner state. It never reads files or signals children.
func (s *Runtime) observationRunLocked(o SelectedObservation) (*ownedRun, error) {
	run, err := s.selectedRunLocked(o.Selection)
	if err != nil {
		return nil, err
	}
	snapshot := run.descriptor.Snapshot()
	if s.disabled || !run.published || snapshot.PublicationUncertain || snapshot.State != domain.RunLive {
		return nil, ErrRuntimeRejected
	}
	metadata := o.Metadata.Snapshot()
	profile := snapshot.Profile.Snapshot()
	if metadata.Host != profile.Host || metadata.HostVersion != profile.Version {
		return nil, domain.ErrChanged
	}
	return run, nil
}

// CheckObservation is cheap admission only. Physical validation belongs to consumption.
func (s *Runtime) CheckObservation(ctx context.Context, o SelectedObservation) error {
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("admit selected observation: %w", err)
	}
	if err := validateSelectedObservation(o); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("admit selected observation: %w", err)
	}
	_, err := s.observationRunLocked(o)
	return err
}

// RecordObservation performs bounded-context physical validation outside the owner lock,
// then consumes untrusted telemetry without changing launch, core Session or control state.
// The existing daemon worker, not a second drainer, supplies its consumption deadline.
func (s *Runtime) RecordObservation(ctx context.Context, o SelectedObservation) error {
	receipt, err := s.AdmitObservation(ctx, o)
	if err != nil {
		return err
	}
	return s.ConsumeObservation(ctx, receipt)
}

// ConsumeObservation preserves original local intake across queueing and physical
// validation. A receipt is private to this actual owner, not a native proof.
func (s *Runtime) ConsumeObservation(ctx context.Context, receipt ObservationReceipt) error {
	if receipt.owner == nil {
		return domain.ErrInvalid
	}
	if receipt.owner != s {
		return domain.ErrChanged
	}
	o := receipt.selected
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("consume selected observation: %w", err)
	}
	if err := validateSelectedObservation(o); err != nil {
		return err
	}
	s.mu.Lock()
	run, err := s.observationRunLocked(o)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if run != receipt.run {
		s.mu.Unlock()
		return domain.ErrChanged
	}
	target := run.descriptor.Target()
	s.mu.Unlock()
	if err = s.registry.Revalidate(ctx, target, string(o.Selection.Profile)); err != nil {
		s.discardObservation(o.Selection, run)
		return xerrors.Errorf("validate observation source: %w", err)
	}
	if err = ctx.Err(); err != nil {
		s.discardObservation(o.Selection, run)
		return xerrors.Errorf("consume selected observation: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		run.clearObservation()
		s.notifyProjectionLocked()
		return xerrors.Errorf("consume selected observation: %w", err)
	}
	current, err := s.observationRunLocked(o)
	if err != nil {
		return err
	}
	if current != run {
		return domain.ErrChanged
	}
	run.observation = o.Metadata
	run.hasObservation = true
	// Native projection capacity/failure must never turn ordinary metadata
	// delivery into failure or stop updating the existing live sample.
	run.recordUnboundProjection(o, receipt.received, s.observationClockLocked())
	s.notifyProjectionLocked()
	return nil
}

// LastObservation returns an untrusted last-arrival sample, never a fresh causal event.
// It intentionally exposes cleared state after Wait without inventing interaction freshness.
// Historical projection metadata is separate and may remain after this live sample clears.
func (s *Runtime) LastObservation(selection OutputSelection) (domain.DerivedObservation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.selectedRunLocked(selection)
	if err != nil {
		return domain.DerivedObservation{}, false, err
	}
	return run.observation, run.hasObservation, nil
}
func (r *ownedRun) clearObservation() {
	r.observation = domain.DerivedObservation{}
	r.hasObservation = false
	if r.projection != nil {
		r.projection.Invalidate(domain.ProjectionGap)
	}
}
func (r *ownedRun) markUncertain() {
	_ = r.descriptor.MarkUncertain()
	r.published = false
	r.clearObservation()
}

// discardObservation never clears a replacement owner that happens to reuse a selection.
func (s *Runtime) discardObservation(selection OutputSelection, run *ownedRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[selection.Identity.Run] == run {
		run.clearObservation()
		s.notifyProjectionLocked()
	}
}
