//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	"slices"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func (s *daemonServer) updateReply(ctx context.Context, reply *runtimeResponse, r *lifecycleRequestRecord, operation string) {
	switch operation {
	case "update_status":
		if r.UpdateStatus.Epoch != s.epoch {
			reply.Code = "changed"
			return
		}
		status, err := s.runtime.UpdateStatus(ctx)
		reply.Code = operationCode(err)
		if err == nil && status.Valid() {
			reply.Lifecycle = &lifecycleResponseRecord{UpdateStatus: recordUpdateStatus(status)}
		}
	case "update_attempt":
		if r.UpdateAttempt.Epoch != s.epoch {
			reply.Code = "changed"
			return
		}
		result, err := s.runtime.AttemptUpdate(ctx, r.UpdateAttempt.Target.target())
		reply.Code = operationCode(err)
		if result.Valid() {
			reply.Lifecycle = &lifecycleResponseRecord{UpdateAttempt: recordUpdateAttempt(result)}
		}
	case "update_handoff":
		request, _ := r.UpdateHandoff.request()
		result, err := s.runtime.HandoffUpdate(ctx, request)
		reply.Code = operationCode(err)
		if lifecycleBindingValid(result.Binding) {
			reply.Lifecycle = &lifecycleResponseRecord{UpdateHandoff: &updateHandoffResultRecord{Target: r.UpdateHandoff.Target, Decision: recordShutdown(result)}}
		}
	default:
		reply.Code = "unsupported"
	}
}

// UpdateStatus reads current owner metadata; never starts a daemon or publishes
// a notice. Overdue is server clock metadata, not proof of native-event age.
func (d *LocalDaemon) UpdateStatus(ctx context.Context, root, epoch string) (app.UpdateStatus, error) {
	if !validLifecycleID(epoch) {
		return app.UpdateStatus{}, app.ErrRuntimeProtocol
	}
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "update_status", Lifecycle: &lifecycleRequestRecord{UpdateStatus: &updateStatusRequestRecord{Epoch: epoch}}}, ManagementRuntimeBudget)
	if err != nil {
		return app.UpdateStatus{}, err
	}
	if !lifecycleOnlyReply(reply) || reply.Code != "ok" || reply.Lifecycle == nil || reply.Lifecycle.UpdateStatus == nil {
		return app.UpdateStatus{}, app.ErrRuntimeProtocol
	}
	status, e := reply.Lifecycle.UpdateStatus.status()
	if e != nil || status.Epoch != epoch {
		return app.UpdateStatus{}, app.ErrRuntimeProtocol
	}
	return status, nil
}

func unknownUpdate(target domain.UpdateTarget) (app.UpdateAttemptResult, error) {
	return app.UpdateAttemptResult{Target: target, Notice: true}, errors.Join(app.ErrUpdateUnknown, app.ErrRuntimeProtocol)
}

// AttemptUpdate makes ONE correlated metadata request; possible post-send
// failure never automatically retargets/retries or masquerades as rejection.
func (d *LocalDaemon) AttemptUpdate(ctx context.Context, root string, request app.UpdateAttemptRequest) (app.UpdateAttemptResult, error) {
	if !request.Valid() {
		return app.UpdateAttemptResult{}, app.ErrRuntimeProtocol
	}
	r := &updateAttemptRequestRecord{Epoch: request.Epoch, Target: recordUpdateTarget(request.Target)}
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: "update_attempt", Lifecycle: &lifecycleRequestRecord{UpdateAttempt: r}}, ManagementRuntimeBudget)
	if !sent && err != nil {
		return app.UpdateAttemptResult{}, err
	}
	if uncertainLifecycleExchange(sent, reply, err) || !lifecycleOnlyReply(reply) {
		return unknownUpdate(request.Target)
	}
	if reply.Lifecycle == nil {
		if err != nil && knownManagementRejection(reply.Code) {
			return app.UpdateAttemptResult{}, err
		}
		return unknownUpdate(request.Target)
	}
	if reply.Lifecycle.UpdateAttempt == nil {
		return unknownUpdate(request.Target)
	}
	result, e := reply.Lifecycle.UpdateAttempt.attempt()
	if e != nil || result.Target != request.Target || result.Status.Epoch != request.Epoch {
		return unknownUpdate(request.Target)
	}
	_, preview := result.Preview.Value()
	switch {
	case reply.Code == "ok" && err == nil:
		if preview != result.Status.Quiescent {
			return unknownUpdate(request.Target)
		}
	case reply.Code == "update_unknown" && errors.Is(err, app.ErrUpdateUnknown):
		if preview || result.Status.Quiescent || !slices.Contains(result.Status.Blockers, "publication_uncertain") || !slices.Contains(result.Status.Blockers, "admission_closed") {
			return unknownUpdate(request.Target)
		}
	case knownManagementRejection(reply.Code) && err != nil:
		if preview {
			return unknownUpdate(request.Target)
		}
	default:
		return unknownUpdate(request.Target)
	}
	return result, err
}

// HandoffUpdate consents only to exact-empty owner exit, never live work loss,
// installation or resume. Read the original shutdown receipt after uncertainty.
func (d *LocalDaemon) HandoffUpdate(ctx context.Context, root string, request app.UpdateHandoffRequest) (app.ShutdownResult, error) {
	if !request.Valid() {
		return app.ShutdownResult{}, app.ErrRuntimeProtocol
	}
	r := &updateHandoffRequestRecord{Target: recordUpdateTarget(request.Target), Binding: recordShutdownBinding(request.Binding), Acknowledged: true}
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: "update_handoff", Lifecycle: &lifecycleRequestRecord{UpdateHandoff: r}}, ManagementRuntimeBudget)
	if !sent && err != nil {
		return app.ShutdownResult{Binding: request.Binding, Outcome: domain.ShutdownRejected}, err
	}
	if uncertainLifecycleExchange(sent, reply, err) || !lifecycleOnlyReply(reply) {
		return unknownShutdown(request.Binding)
	}
	if reply.Lifecycle == nil {
		if err != nil && knownManagementRejection(reply.Code) {
			return app.ShutdownResult{Binding: request.Binding, Outcome: domain.ShutdownRejected}, err
		}
		return unknownShutdown(request.Binding)
	}
	if reply.Lifecycle.UpdateHandoff == nil || reply.Lifecycle.UpdateHandoff.Target.target() != request.Target {
		return unknownShutdown(request.Binding)
	}
	result, e := reply.Lifecycle.UpdateHandoff.Decision.decision()
	if e != nil || result.Binding != request.Binding || result.ClosedTerminals != 0 {
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
