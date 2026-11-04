//go:build darwin || linux

package runtime

import (
	"context"
	"errors"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

var _ app.ResumeClient = (*LocalDaemon)(nil)

func (s *daemonServer) resumeReply(reply *runtimeResponse, record *resumeRequestRecord) {
	if s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	request, err := record.request()
	if err != nil {
		reply.Code = "invalid"
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, ManagementRuntimeBudget)
	defer cancel()
	result, err := s.runtime.Resume(ctx, request)
	reply.Code = operationCode(err)
	reply.Resume = recordResumeResult(request, result)
}

// Resume sends one explicit operation to an existing private management socket.
// A lost/malformed post-send result is unknown, never a reason to resend Start.
func (d *LocalDaemon) Resume(ctx context.Context, root string, request app.ResumeRequest) (app.ResumeResult, error) {
	if !request.Valid() {
		return app.ResumeResult{}, domain.ErrInvalid
	}
	unknown := app.ResumeResult{Phase: domain.ResumeUnknown}
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: "resume", Resume: recordResumeRequest(request)}, ManagementRuntimeBudget)
	if sent && (errors.Is(err, app.ErrDaemonUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || reply.ID == "" && (errors.Is(err, app.ErrRuntimeProtocol) || errors.Is(err, app.ErrRuntimeUnsupported))) {
		return unknown, app.ErrLaunchUnknown
	}
	invalid := errors.Join(app.ErrLaunchUnknown, app.ErrRuntimeProtocol)
	if reply.History != nil || reply.HistoryHint != nil || reply.Control != nil || reply.Output != nil || reply.Status != nil || reply.Page != nil || reply.Run != nil || reply.Role != "" {
		return unknown, invalid
	}
	if reply.ID != "" {
		switch reply.Code {
		case "ok", "launch_unknown", "changed", "limit", "invalid", "not_found", "unsafe", "rejected", "unsupported", "busy":
		default:
			return unknown, invalid
		}
	}
	if reply.Resume == nil {
		if err != nil {
			if errors.Is(err, app.ErrLaunchUnknown) {
				return unknown, err
			}
			return app.ResumeResult{}, err
		}
		return unknown, invalid
	}
	result, decodeErr := reply.Resume.result(request)
	if decodeErr != nil {
		return unknown, invalid
	}
	if err == nil {
		if reply.Code != "ok" || result.Phase != domain.ResumeConfirmed {
			return unknown, invalid
		}
	} else if errors.Is(err, app.ErrLaunchUnknown) {
		if result.Phase != domain.ResumeReserved && result.Phase != domain.ResumeStarted && result.Phase != domain.ResumeUnknown {
			return unknown, invalid
		}
	} else if result.Phase != domain.ResumeFailedBeforeStart && (result.Phase != domain.ResumeUnknown || result.Selection != (app.OutputSelection{}) || result.Owned) {
		return unknown, invalid
	}
	return result, err
}
