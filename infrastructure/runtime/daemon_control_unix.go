//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func (s *daemonServer) controlReply(reply *runtimeResponse, record *controlRequestRecord, operation string) {
	if s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	request, err := record.request(operation)
	record.Data = ""
	if err != nil {
		reply.Code = "invalid"
		return
	}
	defer clear(request.Data)
	ctx, cancel := context.WithTimeout(s.ctx, ManagementRuntimeBudget)
	defer cancel()
	result, err := s.runtime.Control(ctx, request)
	reply.Code = operationCode(err)
	reply.Control = recordControlResult(result)
}
func (s *daemonServer) receiptReply(reply *runtimeResponse, record *controlReceiptRequestRecord) {
	if s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	selection, err := record.Selection.selection()
	if err != nil {
		reply.Code = "invalid"
		return
	}
	if _, err = domain.ControlRequestIDFrom(record.ID); err != nil {
		reply.Code = "invalid"
		return
	}
	result, err := s.runtime.Receipt(selection, record.ID)
	reply.Code = operationCode(err)
	reply.Control = recordControlResult(result)
}
func unknownControlResult(request app.ControlRequest) app.ControlResult {
	return app.ControlResult{Selection: request.Selection, Receipt: domain.ControlSnapshot{Identity: request.Selection.Identity, Intent: request.Intent.Snapshot(), Outcome: domain.ControlUnknown}}
}

// Control sends exactly one explicit operation; a possible post-send loss is unknown, not retryable.
func (d *LocalDaemon) Control(ctx context.Context, root string, request app.ControlRequest) (app.ControlResult, error) {
	if _, err := domain.ControlIntentFrom(request.Intent.Snapshot()); err != nil {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	if len(request.Data) != request.Intent.Snapshot().InputBytes {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	record := recordControlRequest(request)
	validated, validationErr := record.request(string(request.Intent.Snapshot().Kind))
	if validationErr != nil {
		return app.ControlResult{}, validationErr
	}
	clear(validated.Data)
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: string(request.Intent.Snapshot().Kind), Control: record}, ManagementRuntimeBudget)
	// The exchange worker retains this immutable encoded request until completion;
	// never mutate it after caller timeout (the worker may still be marshaling).
	if sent && (errors.Is(err, app.ErrDaemonUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || reply.ID == "" && (errors.Is(err, app.ErrRuntimeProtocol) || errors.Is(err, app.ErrRuntimeUnsupported))) {
		return unknownControlResult(request), app.ErrControlUnknown
	}
	if reply.Lifecycle != nil || reply.Run != nil || reply.Page != nil || reply.Output != nil || reply.Status != nil || reply.Role != "" {
		return unknownControlResult(request), errors.Join(app.ErrControlUnknown, app.ErrRuntimeProtocol)
	}
	if reply.ID != "" {
		switch reply.Code {
		case "ok", "control_unknown", "changed", "limit", "invalid", "not_found", "unsafe", "rejected", "unsupported", "busy":
		default:
			return unknownControlResult(request), errors.Join(app.ErrControlUnknown, app.ErrRuntimeProtocol)
		}
	}
	if reply.Control == nil {
		if err != nil {
			if errors.Is(err, app.ErrControlUnknown) {
				return unknownControlResult(request), err
			}
			return app.ControlResult{}, err
		}
		return unknownControlResult(request), errors.Join(app.ErrControlUnknown, app.ErrRuntimeProtocol)
	}

	result, decodeErr := reply.Control.result()
	if decodeErr != nil || result.Selection != request.Selection || result.Receipt.Intent != request.Intent.Snapshot() || result.Receipt.Outcome == domain.ControlPending || err == nil && (reply.Code != "ok" || result.Receipt.Outcome != domain.ControlDelivered) || err != nil && !errors.Is(err, app.ErrControlUnknown) && result.Receipt.Outcome != domain.ControlRejected || errors.Is(err, app.ErrControlUnknown) && result.Receipt.Outcome != domain.ControlUnknown {
		return unknownControlResult(request), errors.Join(app.ErrControlUnknown, app.ErrRuntimeProtocol)
	}
	return result, err
}

// Receipt reads existing safe evidence without creating a native control effect.
func (d *LocalDaemon) Receipt(ctx context.Context, root string, selection app.OutputSelection, id string) (app.ControlResult, error) {
	if _, err := recordSelection(selection).selection(); err != nil {
		return app.ControlResult{}, err
	}
	if _, err := domain.ControlRequestIDFrom(id); err != nil {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "receipt", Receipt: &controlReceiptRequestRecord{Selection: recordSelection(selection), ID: id}}, ManagementRuntimeBudget)
	if err != nil {
		return app.ControlResult{}, err
	}
	if reply.Lifecycle != nil || reply.Code != "ok" || reply.Control == nil || reply.Run != nil || reply.Page != nil || reply.Output != nil || reply.Status != nil || reply.Role != "" {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	result, err := reply.Control.result()
	if err != nil || result.Selection != selection || result.Receipt.Intent.RequestID != id {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	return result, nil
}
