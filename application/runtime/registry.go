// Package runtime exposes UI-independent registry and execution services.
package runtime

import (
	"context"
	"golang.org/x/xerrors"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// RegistryRepository serializes whole-aggregate updates without filesystem details.
type RegistryRepository interface {
	Load(context.Context) (*domain.Registry, error)
	Update(context.Context, func(*domain.Registry) error) error
}

// RegistryResolver resolves physical evidence without exposing OS or Git DTOs.
type RegistryResolver interface {
	ResolveProject(context.Context, string) (domain.Project, error)
	ResolveWorktree(context.Context, domain.Project, string, string) (domain.Worktree, error)
}

// Registry owns object-level operations, shared by daemon and future UI consumers.
type Registry struct {
	repository RegistryRepository
	resolver   RegistryResolver
}

// NewRegistry constructs the shared service with explicit ports.
func NewRegistry(repository RegistryRepository, resolver RegistryResolver) (*Registry, error) {
	if repository == nil || resolver == nil {
		return nil, domain.ErrInvalid
	}
	return &Registry{repository, resolver}, nil
}

// RegisterProject resolves a canonical project and registers it atomically.
func (s *Registry) RegisterProject(ctx context.Context, path string) (domain.Project, error) {
	p, err := s.resolver.ResolveProject(ctx, path)
	if err != nil {
		return domain.Project{}, xerrors.Errorf("register runtime project: %w", err)
	}
	err = s.repository.Update(ctx, func(r *domain.Registry) error { return r.AddProject(p) })
	if err != nil {
		return domain.Project{}, xerrors.Errorf("register runtime project: %w", err)
	}
	return p, nil
}

// RegisterWorktree binds one owned worktree/store to an unchanged registered project.
func (s *Registry) RegisterWorktree(ctx context.Context, id domain.ProjectID, path, store string) (domain.Worktree, error) {
	var result domain.Worktree
	err := s.repository.Update(ctx, func(r *domain.Registry) error {
		p, err := r.Project(id)
		if err != nil {
			return xerrors.Errorf("validate runtime registry identity: %w", err)
		}
		fresh, err := s.resolver.ResolveProject(ctx, p.Root())
		if err != nil {
			return xerrors.Errorf("validate runtime registry identity: %w", err)
		}
		if fresh != p {
			return domain.ErrChanged
		}
		w, err := s.resolver.ResolveWorktree(ctx, p, path, store)
		if err != nil {
			return xerrors.Errorf("validate runtime registry identity: %w", err)
		}
		if err = r.AddWorktree(w); err != nil {
			return xerrors.Errorf("validate runtime registry identity: %w", err)
		}
		result = w
		return nil
	})
	if err != nil {
		return domain.Worktree{}, xerrors.Errorf("register runtime worktree: %w", err)
	}
	return result, nil
}

// List returns defensive metadata snapshots, never terminal bodies or native secrets.
func (s *Registry) List(ctx context.Context) (domain.RegistryState, error) {
	r, err := s.repository.Load(ctx)
	if err != nil {
		return domain.RegistryState{}, xerrors.Errorf("read runtime registry: %w", err)
	}
	return r.Snapshot(), nil
}

// Export is a read-only metadata snapshot; cancellation and errors never mutate state.
func (s *Registry) Export(ctx context.Context) (domain.RegistryState, error) { return s.List(ctx) }

// Resolve constructs an immutable profile-specific selection and checks it now.
// The profile owner must additionally validate the executable/version before spawn.
func (s *Registry) Resolve(ctx context.Context, id domain.WorktreeID, profile string) (domain.Target, error) {
	r, err := s.repository.Load(ctx)
	if err != nil {
		return domain.Target{}, xerrors.Errorf("resolve runtime target: %w", err)
	}
	target, err := r.Target(id, profile)
	if err != nil {
		return domain.Target{}, xerrors.Errorf("resolve runtime target: %w", err)
	}
	if err = s.Revalidate(ctx, target, profile); err != nil {
		return domain.Target{}, xerrors.Errorf("resolve runtime target: %w", err)
	}
	return target, nil
}

// Revalidate checks the stored selection, profile expectation and fresh physical evidence.
// Callers must retain the returned target and repeat this immediately before control.
func (s *Registry) Revalidate(ctx context.Context, target domain.Target, profile string) error {
	expected, err := domain.ProfileIDFrom(profile)
	if err != nil {
		return xerrors.Errorf("validate runtime registry identity: %w", err)
	}
	if target.Profile() != expected {
		return domain.ErrChanged
	}
	r, err := s.repository.Load(ctx)
	if err != nil {
		return xerrors.Errorf("validate runtime registry identity: %w", err)
	}
	stored, err := r.Worktree(target.Worktree().ID())
	if err != nil {
		return xerrors.Errorf("validate runtime registry identity: %w", err)
	}
	if stored != target.Worktree() {
		return domain.ErrChanged
	}
	p, err := r.Project(stored.Project())
	if err != nil {
		return xerrors.Errorf("validate runtime registry identity: %w", err)
	}
	fresh, err := s.resolver.ResolveProject(ctx, p.Root())
	if err != nil {
		return xerrors.Errorf("validate runtime registry identity: %w", err)
	}
	if fresh != p {
		return domain.ErrChanged
	}
	w, err := s.resolver.ResolveWorktree(ctx, p, stored.Path(), stored.Store())
	if err != nil {
		return xerrors.Errorf("validate runtime registry identity: %w", err)
	}
	if w != stored {
		return domain.ErrChanged
	}
	return nil
}

// PlanDeleteProject previews non-cascading metadata removal without touching Git.
func (s *Registry) PlanDeleteProject(ctx context.Context, id domain.ProjectID) (domain.DeletePlan, error) {
	r, err := s.repository.Load(ctx)
	if err != nil {
		return domain.DeletePlan{}, xerrors.Errorf("plan registry removal: %w", err)
	}
	plan, err := r.PlanDeleteProject(id)
	if err != nil {
		return domain.DeletePlan{}, xerrors.Errorf("plan project removal: %w", err)
	}
	return plan, nil
}

// PlanDeleteWorktree previews removal; abandoning the plan is a no-op.
func (s *Registry) PlanDeleteWorktree(ctx context.Context, id domain.WorktreeID) (domain.DeletePlan, error) {
	r, err := s.repository.Load(ctx)
	if err != nil {
		return domain.DeletePlan{}, xerrors.Errorf("plan registry removal: %w", err)
	}
	plan, err := r.PlanDeleteWorktree(id)
	if err != nil {
		return domain.DeletePlan{}, xerrors.Errorf("plan worktree removal: %w", err)
	}
	return plan, nil
}

// ApplyDelete applies only the exact revision/content-bound explicit plan.
func (s *Registry) ApplyDelete(ctx context.Context, plan domain.DeletePlan) error {
	err := s.repository.Update(ctx, func(r *domain.Registry) error { return r.ApplyDelete(plan) })
	if err != nil {
		return xerrors.Errorf("apply registry removal: %w", err)
	}
	return nil
}
