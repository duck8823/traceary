package runtime

import (
	"context"
	"encoding/hex"
	"strings"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// SelectedObservation joins explicit correlation to an existing immutable owned launch.
// Matching selection alone is never proof of native provenance or readiness.
type SelectedObservation struct {
	Selection OutputSelection
	Metadata  domain.DerivedObservation
}

func validateSelectedObservation(o SelectedObservation) error {
	if _, err := domain.DerivedObservationOf(o.Metadata.Snapshot()); err != nil {
		return xerrors.Errorf("validate selected observation metadata: %w", err)
	}
	if !o.Selection.Identity.Valid() {
		return domain.ErrInvalid
	}
	worktree := string(o.Selection.Worktree)
	if len(worktree) != 66 || !strings.HasPrefix(worktree, "w_") || worktree != strings.ToLower(worktree) {
		return domain.ErrInvalid
	}
	if _, err := hex.DecodeString(worktree[2:]); err != nil {
		return domain.ErrInvalid
	}
	if _, err := domain.ProfileIDFrom(string(o.Selection.Profile)); err != nil {
		return xerrors.Errorf("validate observation profile: %w", err)
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
	return nil
}

// LastObservation returns an untrusted last-arrival sample, never a fresh causal event.
// It intentionally exposes cleared state after Wait without inventing interaction freshness.
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
	}
}
