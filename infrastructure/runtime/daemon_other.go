//go:build !darwin && !linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// Serve fails closed on platforms without a certified native peer adapter.
func (d *LocalDaemon) Serve(context.Context, string) error { return app.ErrRuntimeUnsupported }

// Status never falls back to an unchecked transport.
func (d *LocalDaemon) Status(context.Context, string) (app.DaemonStatus, error) {
	return app.DaemonStatus{}, app.ErrRuntimeUnsupported
}

// Observe never uses TCP or an unchecked peer fallback.
func (d *LocalDaemon) Observe(context.Context, string, app.ObservationMetadata) error {
	return app.ErrRuntimeUnsupported
}

// Launch has no unchecked process/transport fallback.
func (d *LocalDaemon) Launch(context.Context, string, app.LaunchRequest) (domain.RunSnapshot, error) {
	return domain.RunSnapshot{}, app.ErrRuntimeUnsupported
}

// ListRuns never treats saved PIDs as live process ownership.
func (d *LocalDaemon) ListRuns(context.Context, string, app.RunPageRequest) (app.RunPage, error) {
	return app.RunPage{}, app.ErrRuntimeUnsupported
}
