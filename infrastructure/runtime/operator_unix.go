//go:build darwin || linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	"golang.org/x/xerrors"
)

// OpenMetadata never initializes core data, creates a root, or starts a daemon.
func (LocalOperatorFactory) OpenMetadata(ctx context.Context, root string) (app.MetadataAudit, error) {
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("open metadata audit: %w", err)
	}
	store, err := OpenMetadataStore(root, MaxRegistryBytes)
	if err != nil {
		return nil, xerrors.Errorf("open metadata audit: %w", err)
	}
	return store, nil
}

// Create creates the explicitly selected private operator metadata root.
func (LocalOperatorFactory) Create(ctx context.Context, root string) (app.OperatorServices, error) {
	return openOperator(ctx, root, true)
}

// Open reads an existing root; missing directories are never created.
func (LocalOperatorFactory) Open(ctx context.Context, root string) (app.OperatorServices, error) {
	return openOperator(ctx, root, false)
}
func openOperator(ctx context.Context, root string, create bool) (app.OperatorServices, error) {
	if err := ctx.Err(); err != nil {
		return app.OperatorServices{}, xerrors.Errorf("open operator services: %w", err)
	}
	var metadata *RegistryStore
	var err error
	if create {
		metadata, err = NewRegistryStore(root, MaxRegistryBytes)
	} else {
		metadata, err = OpenRegistryStore(root, MaxRegistryBytes)
	}
	if err != nil {
		return app.OperatorServices{}, err
	}
	registry, err := app.NewRegistry(metadata, RegistryResolver{})
	if err != nil {
		return app.OperatorServices{}, xerrors.Errorf("compose runtime registry: %w", err)
	}
	execution, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		return app.OperatorServices{}, err
	}
	inspector, err := NewPTYLauncher(registry, nil)
	if err != nil {
		return app.OperatorServices{}, err
	}
	profiles, err := app.NewProfiles(inspector, execution)
	if err != nil {
		return app.OperatorServices{}, xerrors.Errorf("compose runtime profiles: %w", err)
	}
	return app.OperatorServices{Registry: registry, Profiles: profiles, Lifecycle: execution}, nil
}

// NewManagedRuntime creates an empty owner for this daemon epoch, never adopting persisted PIDs.
func NewManagedRuntime(ctx context.Context, root, epoch string) (*app.Runtime, error) {
	services, err := (LocalOperatorFactory{}).Open(ctx, root)
	if err != nil {
		return nil, err
	}
	execution, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		return nil, err
	}
	launcher, err := NewPTYLauncherWithHistoryRoot(services.Registry, nil, root)
	if err != nil {
		return nil, err
	}
	// Capabilities are fixed for this daemon epoch. Merely implementing PTY
	// launch must not advertise recovery for unrelated/unqualified hosts.
	var processes app.ProcessLauncher = launcher
	profiles, err := execution.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		if _, err := inspectClaudeRecoverySource(ctx, profile, launcher.environment); err == nil {
			processes = &claudeNativeLauncher{PTYLauncher: launcher}
			break
		}
	}
	owner, err := app.NewRuntime(services.Registry, execution, processes, execution, epoch)
	if err != nil {
		return nil, xerrors.Errorf("compose managed runtime: %w", err)
	}
	if _, err = owner.InitializeLifecycle(ctx); err != nil {
		return nil, xerrors.Errorf("publish managed runtime epoch: %w", err)
	}
	return owner, nil
}
