package runtime

import (
	"context"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ProfileInspector validates explicit operator policy, never launch-supplied commands.
type ProfileInspector interface {
	InspectProfile(context.Context, string, string, string, string) (domain.HostProfile, error)
}

// ProfileRepository stores immutable inspected profiles separately from credentials.
type ProfileRepository interface {
	ProfileCatalog
	RegisterProfile(context.Context, domain.HostProfile) error
	ListProfiles(context.Context) ([]domain.HostProfile, error)
}

// Profiles owns explicit registration and safe operator reads.
type Profiles struct {
	inspector  ProfileInspector
	repository ProfileRepository
}

// NewProfiles assembles the object-level profile service.
func NewProfiles(inspector ProfileInspector, repository ProfileRepository) (*Profiles, error) {
	if inspector == nil || repository == nil {
		return nil, domain.ErrInvalid
	}
	return &Profiles{inspector, repository}, nil
}

// Register inspects the real executable before persisting immutable policy.
func (s *Profiles) Register(ctx context.Context, id, host, executable, version string) (domain.HostProfile, error) {
	profile, err := s.inspector.InspectProfile(ctx, id, host, executable, version)
	if err != nil {
		return domain.HostProfile{}, xerrors.Errorf("register operator profile: %w", err)
	}
	if err = s.repository.RegisterProfile(ctx, profile); err != nil {
		return domain.HostProfile{}, xerrors.Errorf("register operator profile: %w", err)
	}
	return profile, nil
}

// List returns metadata only; it never runs a native host.
func (s *Profiles) List(ctx context.Context) ([]domain.HostProfile, error) {
	profiles, err := s.repository.ListProfiles(ctx)
	if err != nil {
		return nil, xerrors.Errorf("list operator profiles: %w", err)
	}
	return profiles, nil
}

// OperatorServices are local operator capabilities, not management RPC overrides.
type OperatorServices struct {
	Lifecycle LifecycleReader
	Registry  *Registry
	Profiles  *Profiles
}

// OperatorFactory separates explicit creation from existing-root reads.
type OperatorFactory interface {
	Create(context.Context, string) (OperatorServices, error)
	Open(context.Context, string) (OperatorServices, error)
}

// Factory creates an empty epoch owner; persisted runs are never adopted.
type Factory func(context.Context, string, string) (*Runtime, error)

// RunPageRequest binds continuation to one epoch and unchanged runtime revision.
type RunPageRequest struct {
	Epoch, Cursor string
	Revision      uint64
}

// RunPage is a bounded metadata view, not a liveness/approval credential.
type RunPage struct {
	Epoch    string
	Revision uint64
	Runs     []domain.RunSnapshot
	Next     string
}

// Client exposes only named target-bound management calls.
type Client interface {
	Launch(context.Context, string, LaunchRequest) (domain.RunSnapshot, error)
	ListRuns(context.Context, string, RunPageRequest) (RunPage, error)
}
