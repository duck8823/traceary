//go:build darwin || linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	"golang.org/x/xerrors"
)

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
	return app.OperatorServices{Registry: registry, Profiles: profiles}, nil
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
	launcher, err := NewPTYLauncher(services.Registry, nil)
	if err != nil {
		return nil, err
	}
	owner, err := app.NewRuntime(services.Registry, execution, launcher, execution, epoch)
	if err != nil {
		return nil, xerrors.Errorf("compose managed runtime: %w", err)
	}
	return owner, nil
}
