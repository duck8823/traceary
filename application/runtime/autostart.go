package runtime

import (
	"context"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// AutoStartPreference changes only the local opt-in, never epoch or host state.
type AutoStartPreference interface {
	SetAutoStartOptIn(context.Context, bool) error
}

// AutoStartDescriptor renders the current executable's descriptor, without installation.
// Platform and executable selection belong exclusively to the trusted adapter.
type AutoStartDescriptor interface {
	RenderAutoStartDescriptor(context.Context) (string, error)
}

// AutoStartFactory distinguishes existing-root reads from explicit preference creation.
// It exposes no registry, launch, epoch, shutdown or installer capabilities.
type AutoStartFactory interface {
	OpenAutoStart(context.Context, string) (*AutoStart, error)
	CreateAutoStart(context.Context, string) (*AutoStart, error)
}

// AutoStartStatus is local configuration, not a claim about the host's installed jobs.
type AutoStartStatus struct {
	OptIn             bool   `json:"opt_in"`
	InstallationState string `json:"installation_state"`
	HostInspection    string `json:"host_inspection"`
	StartsAgents      bool   `json:"starts_agents"`
}

// AutoStart owns explicit preference and read-only descriptor operations.
type AutoStart struct {
	reader     LifecycleReader
	preference AutoStartPreference
	descriptor AutoStartDescriptor
}

// NewAutoStart assembles metadata-only auto-start capabilities.
func NewAutoStart(reader LifecycleReader, preference AutoStartPreference, descriptor AutoStartDescriptor) (*AutoStart, error) {
	if reader == nil || preference == nil || descriptor == nil {
		return nil, domain.ErrInvalid
	}
	return &AutoStart{reader: reader, preference: preference, descriptor: descriptor}, nil
}
func autoStartStatus(enabled bool) AutoStartStatus {
	return AutoStartStatus{OptIn: enabled, InstallationState: "unknown", HostInspection: "not_performed", StartsAgents: false}
}

// Status reads local metadata only; it neither scans the host nor creates a root.
func (s *AutoStart) Status(ctx context.Context) (AutoStartStatus, error) {
	state, err := s.reader.ReadLifecycle(ctx)
	if err != nil {
		return AutoStartStatus{}, xerrors.Errorf("read auto-start preference: %w", err)
	}
	return autoStartStatus(state.AutoStartOptIn), nil
}

// SetOptIn persists one explicit preference, without installing or starting anything.
// An uncertain publication is returned to the caller without automatic retry.
func (s *AutoStart) SetOptIn(ctx context.Context, enabled bool) (AutoStartStatus, error) {
	if err := s.preference.SetAutoStartOptIn(ctx, enabled); err != nil {
		return AutoStartStatus{}, xerrors.Errorf("write auto-start preference: %w", err)
	}
	return autoStartStatus(enabled), nil
}

// Descriptor requires existing opt-in; returning XML never grants installation authority.
func (s *AutoStart) Descriptor(ctx context.Context) (string, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return "", err
	}
	if !status.OptIn {
		return "", xerrors.Errorf("explicit auto-start opt-in required: %w", domain.ErrUnsafe)
	}
	value, err := s.descriptor.RenderAutoStartDescriptor(ctx)
	if err != nil {
		return "", xerrors.Errorf("render auto-start descriptor: %w", err)
	}
	return value, nil
}
