//go:build !darwin && !linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
)

// Create refuses unverified native process/filesystem platforms.
func (LocalOperatorFactory) Create(context.Context, string) (app.OperatorServices, error) {
	return app.OperatorServices{}, app.ErrRuntimeUnsupported
}

// Open never falls back to unchecked metadata or process ownership.
func (LocalOperatorFactory) Open(context.Context, string) (app.OperatorServices, error) {
	return app.OperatorServices{}, app.ErrRuntimeUnsupported
}

// NewManagedRuntime never adopts PIDs or uses a shell fallback.
func NewManagedRuntime(context.Context, string, string) (*app.Runtime, error) {
	return nil, app.ErrRuntimeUnsupported
}
