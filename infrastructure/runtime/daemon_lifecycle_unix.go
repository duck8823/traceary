//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"sort"
)

func (s *daemonServer) lifecycleReply(reply *runtimeResponse, r *lifecycleRequestRecord, operation string) {
	if !r.validOperation(operation) {
		reply.Code = "invalid"
		return
	}
	if s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, ManagementRuntimeBudget)
	defer cancel()
	switch operation {
	case "update_status", "update_attempt", "update_handoff":
		s.updateReply(ctx, reply, r, operation)
	case "shutdown_preview":
		if r.Preview.Epoch != s.epoch {
			reply.Code = "changed"
			return
		}
		preview, err := s.runtime.PreviewShutdown(ctx)
		reply.Code = operationCode(err)
		if err != nil {
			return
		}
		s.shutdownPreviewReply(reply, preview, 0)
	case "shutdown_page":
		binding, _ := r.Page.Binding.binding()
		preview, err := s.runtime.ReadShutdownPreview(binding.Snapshot())
		reply.Code = operationCode(err)
		if err != nil {
			return
		}
		index := sort.Search(len(preview.Runs), func(i int) bool { return preview.Runs[i].Identity.Run >= r.Page.Cursor })
		if index >= len(preview.Runs) || preview.Runs[index].Identity.Run != r.Page.Cursor || index+1 != r.Page.Offset {
			reply.Code = "changed"
			return
		}
		s.shutdownPreviewReply(reply, preview, index+1)
	case "shutdown_cancel":
		binding, _ := r.Cancel.binding()
		err := s.runtime.CancelShutdown(binding.Snapshot())
		reply.Code = operationCode(err)
		if err == nil {
			reply.Lifecycle = &lifecycleResponseRecord{Cancelled: r.Cancel}
		}
	case "shutdown_confirm":
		binding, _ := r.Confirm.Binding.binding()
		result, err := s.runtime.ConfirmShutdown(ctx, app.ShutdownConfirmation{Binding: binding.Snapshot(), WorkLossAcknowledged: true})
		reply.Code = operationCode(err)
		if _, e := domain.ShutdownBindingFrom(result.Binding); e == nil {
			record := recordShutdown(result)
			reply.Lifecycle = &lifecycleResponseRecord{Decision: &record}
		}
	case "shutdown_receipt":
		binding, _ := r.Receipt.binding()
		result, err := s.runtime.ShutdownReceipt(binding.Snapshot())
		reply.Code = operationCode(err)
		if err == nil {
			record := recordShutdown(result)
			reply.Lifecycle = &lifecycleResponseRecord{Decision: &record}
		}
	default:
		reply.Code = "unsupported"
	}
}
func (s *daemonServer) shutdownPreviewReply(reply *runtimeResponse, preview app.ShutdownPreview, start int) {
	end := min(start+2, len(preview.Runs))
	page := app.ShutdownPreviewPage{Binding: preview.Binding, Offset: start, Runs: []app.ShutdownPreviewRun{}}
	for _, snapshot := range preview.Runs[start:end] {
		page.Runs = append(page.Runs, app.ShutdownPreviewRunFrom(snapshot))
	}
	if end < len(preview.Runs) {
		page.Next = page.Runs[len(page.Runs)-1].Selection.Identity.Run
	}
	reply.Lifecycle = &lifecycleResponseRecord{Preview: recordShutdownPage(page)}
	for {
		data, err := json.Marshal(reply)
		if err == nil && len(data) <= MaxRuntimeFrameBytes {
			return
		}
		if len(page.Runs) <= 1 {
			reply.Lifecycle = nil
			reply.Code = "limit"
			return
		}
		page.Runs = page.Runs[:len(page.Runs)-1]
		page.Next = page.Runs[len(page.Runs)-1].Selection.Identity.Run
		reply.Lifecycle.Preview = recordShutdownPage(page)
	}
}
func lifecycleOnlyReply(r runtimeResponse) bool {
	return r.Resume == nil && r.History == nil && r.HistoryHint == nil && r.Control == nil && r.Output == nil && r.Status == nil && r.Run == nil && r.Page == nil && r.Role == "" && (r.Lifecycle == nil || r.Lifecycle.singleArm())
}
func lifecycleBindingValid(s domain.ShutdownBindingState) bool {
	_, err := domain.ShutdownBindingFrom(s)
	return err == nil
}
func knownManagementRejection(code string) bool {
	switch code {
	case "changed", "limit", "invalid", "not_found", "unsafe", "rejected", "unsupported", "busy":
		return true
	default:
		return false
	}
}
func uncertainLifecycleExchange(sent bool, r runtimeResponse, err error) bool {
	return sent && (errors.Is(err, app.ErrDaemonUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || r.ID == "" && (errors.Is(err, app.ErrRuntimeProtocol) || errors.Is(err, app.ErrRuntimeUnsupported)))
}
func unknownShutdown(binding domain.ShutdownBindingState) (app.ShutdownResult, error) {
	return app.ShutdownResult{Binding: binding, Outcome: domain.ShutdownUnknown}, errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
}

// PreviewShutdown creates one non-freezing plan; it never starts a daemon.
func (d *LocalDaemon) PreviewShutdown(ctx context.Context, root, epoch string) (app.ShutdownPreviewPage, error) {
	if !validLifecycleID(epoch) {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "shutdown_preview", Lifecycle: &lifecycleRequestRecord{Preview: &shutdownPreviewRequestRecord{Epoch: epoch}}}, ManagementRuntimeBudget)
	if err != nil {
		return app.ShutdownPreviewPage{}, err
	}
	if !lifecycleOnlyReply(reply) || reply.Code != "ok" || reply.Lifecycle == nil || reply.Lifecycle.Preview == nil || reply.Lifecycle.Decision != nil || reply.Lifecycle.Cancelled != nil {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	page, e := reply.Lifecycle.Preview.page()
	if e != nil || page.Binding.Epoch != epoch || page.Offset != 0 {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	return page, nil
}

// ShutdownPage reads a contiguous continuation of the exact original preview.
func (d *LocalDaemon) ShutdownPage(ctx context.Context, root string, r app.ShutdownPageRequest) (app.ShutdownPreviewPage, error) {
	record := &lifecycleRequestRecord{Page: &shutdownPageRequestRecord{Binding: recordShutdownBinding(r.Binding), Cursor: r.Cursor, Offset: r.Offset}}
	if !record.validOperation("shutdown_page") {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "shutdown_page", Lifecycle: record}, ManagementRuntimeBudget)
	if err != nil {
		return app.ShutdownPreviewPage{}, err
	}
	if !lifecycleOnlyReply(reply) || reply.Code != "ok" || reply.Lifecycle == nil || reply.Lifecycle.Preview == nil || reply.Lifecycle.Decision != nil || reply.Lifecycle.Cancelled != nil {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	page, e := reply.Lifecycle.Preview.page()
	if e != nil || page.Binding != r.Binding || page.Offset != r.Offset || len(page.Runs) > 0 && page.Runs[0].Selection.Identity.Run <= r.Cursor {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	return page, nil
}

// CancelShutdown submits one cancellation, without turning caller loss into a retry.
func (d *LocalDaemon) CancelShutdown(ctx context.Context, root string, binding domain.ShutdownBindingState) error {
	if !lifecycleBindingValid(binding) {
		return app.ErrRuntimeProtocol
	}
	record := recordShutdownBinding(binding)
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: "shutdown_cancel", Lifecycle: &lifecycleRequestRecord{Cancel: &record}}, ManagementRuntimeBudget)
	if uncertainLifecycleExchange(sent, reply, err) {
		return app.ErrShutdownUnknown
	}
	if !sent && err != nil {
		return err
	}
	if !lifecycleOnlyReply(reply) {
		return errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
	}
	if err != nil {
		if knownManagementRejection(reply.Code) && reply.Lifecycle == nil {
			return err
		}
		return errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
	}
	if reply.Code != "ok" || reply.Lifecycle == nil || reply.Lifecycle.Cancelled == nil || reply.Lifecycle.Preview != nil || reply.Lifecycle.Decision != nil {
		return errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
	}
	actual, e := reply.Lifecycle.Cancelled.binding()
	if e != nil || actual.Snapshot() != binding {
		return errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
	}
	return nil
}

// ConfirmShutdown never repeats a possibly accepted irreversible decision.
func (d *LocalDaemon) ConfirmShutdown(ctx context.Context, root string, request app.ShutdownConfirmation) (app.ShutdownResult, error) {
	if !lifecycleBindingValid(request.Binding) || !request.WorkLossAcknowledged {
		return app.ShutdownResult{}, app.ErrRuntimeProtocol
	}
	record := &lifecycleRequestRecord{Confirm: &shutdownConfirmationRecord{Binding: recordShutdownBinding(request.Binding), WorkLossAcknowledged: true}}
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: "shutdown_confirm", Lifecycle: record}, ManagementRuntimeBudget)
	if uncertainLifecycleExchange(sent, reply, err) {
		return app.ShutdownResult{Binding: request.Binding, Outcome: domain.ShutdownUnknown}, app.ErrShutdownUnknown
	}
	if !sent && err != nil {
		return app.ShutdownResult{Binding: request.Binding, Outcome: domain.ShutdownRejected}, err
	}
	if !lifecycleOnlyReply(reply) {
		return unknownShutdown(request.Binding)
	}
	if reply.Lifecycle == nil {
		if err != nil && knownManagementRejection(reply.Code) {
			return app.ShutdownResult{Binding: request.Binding, Outcome: domain.ShutdownRejected}, err
		}
		return unknownShutdown(request.Binding)
	}
	if reply.Lifecycle.Decision == nil || reply.Lifecycle.Preview != nil || reply.Lifecycle.Cancelled != nil {
		return unknownShutdown(request.Binding)
	}
	result, e := reply.Lifecycle.Decision.decision()
	if e != nil || result.Binding != request.Binding {
		return unknownShutdown(request.Binding)
	}
	switch result.Outcome {
	case domain.ShutdownDraining:
		if err != nil || reply.Code != "ok" {
			return unknownShutdown(request.Binding)
		}
	case domain.ShutdownUnknown:
		if !errors.Is(err, app.ErrShutdownUnknown) || reply.Code != "shutdown_unknown" {
			return unknownShutdown(request.Binding)
		}
	case domain.ShutdownRejected:
		if err == nil || !knownManagementRejection(reply.Code) {
			return unknownShutdown(request.Binding)
		}
	default:
		return unknownShutdown(request.Binding)
	}
	return result, err
}

// ShutdownReceipt reads safe original evidence, including pending/unknown, without replay.
func (d *LocalDaemon) ShutdownReceipt(ctx context.Context, root string, binding domain.ShutdownBindingState) (app.ShutdownResult, error) {
	if !lifecycleBindingValid(binding) {
		return app.ShutdownResult{}, app.ErrRuntimeProtocol
	}
	record := recordShutdownBinding(binding)
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "shutdown_receipt", Lifecycle: &lifecycleRequestRecord{Receipt: &record}}, ManagementRuntimeBudget)
	if err != nil {
		return app.ShutdownResult{}, err
	}
	if !lifecycleOnlyReply(reply) || reply.Code != "ok" || reply.Lifecycle == nil || reply.Lifecycle.Decision == nil || reply.Lifecycle.Preview != nil || reply.Lifecycle.Cancelled != nil {
		return app.ShutdownResult{}, app.ErrRuntimeProtocol
	}
	result, e := reply.Lifecycle.Decision.decision()
	if e != nil || result.Binding != binding {
		return app.ShutdownResult{}, app.ErrRuntimeProtocol
	}
	return result, nil
}
