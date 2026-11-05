//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func (s *daemonServer) selectedObservationReply(reply *runtimeResponse, request runtimeRequest, endpoint *daemonSocket, size int) {
	if request.SelectedObservation == nil || !request.singleArm() {
		reply.Code = "invalid"
		return
	}
	if request.Operation != selectedObservationOperation || endpoint.role != "observations" || s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	observation, err := request.SelectedObservation.observation()
	if err != nil {
		reply.Code = "invalid"
		return
	}
	receipt, err := s.runtime.AdmitObservation(s.ctx, observation)
	if err != nil {
		reply.Code = operationCode(err)
		return
	}
	if !s.enqueueItem(pendingObservation{selected: &receipt, bytes: size}) {
		reply.Code = "busy"
		return
	}
	reply.Code = "queued"
}

// ObserveSelected performs one bounded observations-only exchange, without legacy fallback.
func (d *LocalDaemon) ObserveSelected(ctx context.Context, root string, o app.SelectedObservation) error {
	bounded, cancel := context.WithTimeout(ctx, OptionalRuntimeBudget)
	defer cancel()
	record := recordSelectedObservation(o)
	if _, err := record.observation(); err != nil {
		return app.ErrRuntimeProtocol
	}
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > domain.MaxSelectedObservationBytes {
		return app.ErrRuntimeProtocol
	}
	reply, _, err := d.exchange(bounded, root, runtimeRequest{Operation: selectedObservationOperation, SelectedObservation: record}, OptionalRuntimeBudget)
	if err != nil {
		return err
	}
	if reply.Code != "queued" || !reply.acknowledgmentOnly() {
		return app.ErrRuntimeProtocol
	}
	return nil
}
