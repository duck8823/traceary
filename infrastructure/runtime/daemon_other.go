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

// Attach has no unchecked streaming fallback.
func (d *LocalDaemon) Attach(context.Context, string, app.OutputSelection, domain.ByteOffset) (app.OutputStream, error) {
	return nil, app.ErrRuntimeUnsupported
}

// Control never falls back to unchecked process or network access.
func (d *LocalDaemon) Control(context.Context, string, app.ControlRequest) (app.ControlResult, error) {
	return app.ControlResult{}, app.ErrRuntimeUnsupported
}

// Receipt never creates control authority from persisted process identifiers.
func (d *LocalDaemon) Receipt(context.Context, string, app.OutputSelection, string) (app.ControlResult, error) {
	return app.ControlResult{}, app.ErrRuntimeUnsupported
}

// HistoryPage refuses unsupported native transport rather than opening a core store.
func (d *LocalDaemon) HistoryPage(context.Context, string, app.RemoteHistoryRequest) (app.HistoryResult, error) {
	return app.HistoryResult{}, app.ErrRuntimeUnsupported
}

// HintHistory never starts an unsupported daemon or falls back to network transport.
func (d *LocalDaemon) HintHistory(context.Context, string) error { return app.ErrRuntimeUnsupported }

// NewHistoryService never opens unchecked metadata on unsupported platforms.
func NewHistoryService(context.Context, string) (*app.History, error) {
	return nil, app.ErrRuntimeUnsupported
}

// PreviewShutdown has no unchecked non-Unix transport fallback.
func (d *LocalDaemon) PreviewShutdown(context.Context, string, string) (app.ShutdownPreviewPage, error) {
	return app.ShutdownPreviewPage{}, app.ErrRuntimeUnsupported
}

// ShutdownPage has no unchecked non-Unix transport fallback.
func (d *LocalDaemon) ShutdownPage(context.Context, string, app.ShutdownPageRequest) (app.ShutdownPreviewPage, error) {
	return app.ShutdownPreviewPage{}, app.ErrRuntimeUnsupported
}

// CancelShutdown never creates a fallback lifecycle authority.
func (d *LocalDaemon) CancelShutdown(context.Context, string, domain.ShutdownBindingState) error {
	return app.ErrRuntimeUnsupported
}

// ConfirmShutdown has no unchecked process fallback.
func (d *LocalDaemon) ConfirmShutdown(context.Context, string, app.ShutdownConfirmation) (app.ShutdownResult, error) {
	return app.ShutdownResult{}, app.ErrRuntimeUnsupported
}

// ShutdownReceipt never reconstructs process ownership.
func (d *LocalDaemon) ShutdownReceipt(context.Context, string, domain.ShutdownBindingState) (app.ShutdownResult, error) {
	return app.ShutdownResult{}, app.ErrRuntimeUnsupported
}

// UpdateStatus never fabricates unsupported native owner metadata.
func (d *LocalDaemon) UpdateStatus(context.Context, string, string) (app.UpdateStatus, error) {
	return app.UpdateStatus{}, app.ErrRuntimeUnsupported
}

// AttemptUpdate has no unchecked transport or updater fallback.
func (d *LocalDaemon) AttemptUpdate(context.Context, string, app.UpdateAttemptRequest) (app.UpdateAttemptResult, error) {
	return app.UpdateAttemptResult{}, app.ErrRuntimeUnsupported
}

// HandoffUpdate has no unchecked process/update fallback.
func (d *LocalDaemon) HandoffUpdate(context.Context, string, app.UpdateHandoffRequest) (app.ShutdownResult, error) {
	return app.ShutdownResult{}, app.ErrRuntimeUnsupported
}

// ObserveSelected never uses an uncertified platform, TCP, legacy or unchecked peer fallback.
func (d *LocalDaemon) ObserveSelected(context.Context, string, app.SelectedObservation) error {
	return app.ErrRuntimeUnsupported
}
