package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
)

type updateTargetRecord struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

func recordUpdateTarget(t domain.UpdateTarget) updateTargetRecord {
	return updateTargetRecord{t.Version, t.Protocol}
}
func (r updateTargetRecord) target() domain.UpdateTarget {
	return domain.UpdateTarget{Version: r.Version, Protocol: r.Protocol}
}

type updateStatusRequestRecord struct {
	Epoch string `json:"epoch"`
}
type updateAttemptRequestRecord struct {
	Epoch  string             `json:"epoch"`
	Target updateTargetRecord `json:"target"`
}
type updateHandoffRequestRecord struct {
	Target       updateTargetRecord    `json:"target"`
	Binding      shutdownBindingRecord `json:"binding"`
	Acknowledged bool                  `json:"daemon_exit_acknowledged"`
}

func (r updateHandoffRequestRecord) request() (app.UpdateHandoffRequest, error) {
	binding, err := r.Binding.binding()
	if err != nil {
		return app.UpdateHandoffRequest{}, app.ErrRuntimeProtocol
	}
	request := app.UpdateHandoffRequest{Target: r.Target.target(), Binding: binding.Snapshot(), DaemonExitAcknowledged: r.Acknowledged}
	if !request.Valid() {
		return app.UpdateHandoffRequest{}, app.ErrRuntimeProtocol
	}
	return request, nil
}

type updateStatusRecord struct {
	Epoch      string                `json:"epoch"`
	Quiescent  bool                  `json:"quiescent"`
	Blockers   []string              `json:"blockers"`
	Deferral   *updateDeferralRecord `json:"update_deferral,omitempty"`
	Overdue24h bool                  `json:"overdue_24h"`
}

func recordUpdateStatus(s app.UpdateStatus) *updateStatusRecord {
	r := &updateStatusRecord{Epoch: s.Epoch, Quiescent: s.Quiescent, Blockers: append([]string{}, s.Blockers...), Overdue24h: s.Overdue24h}
	if state, found := s.Deferral.Value(); found {
		d := recordDeferral(state)
		r.Deferral = &d
	}
	return r
}
func (r updateStatusRecord) status() (app.UpdateStatus, error) {
	s := app.UpdateStatus{Epoch: r.Epoch, Quiescent: r.Quiescent, Blockers: r.Blockers, Overdue24h: r.Overdue24h}
	if r.Deferral != nil {
		state, err := r.Deferral.deferral()
		if err != nil {
			return app.UpdateStatus{}, app.ErrRuntimeProtocol
		}
		s.Deferral = types.Some(state)
	}
	if !s.Valid() {
		return app.UpdateStatus{}, app.ErrRuntimeProtocol
	}
	return s, nil
}

type updateAttemptResultRecord struct {
	Target  updateTargetRecord     `json:"target"`
	Status  updateStatusRecord     `json:"status"`
	Notice  bool                   `json:"notice"`
	Preview *shutdownBindingRecord `json:"preview,omitempty"`
}

func recordUpdateAttempt(r app.UpdateAttemptResult) *updateAttemptResultRecord {
	result := &updateAttemptResultRecord{Target: recordUpdateTarget(r.Target), Status: *recordUpdateStatus(r.Status), Notice: r.Notice}
	if preview, ok := r.Preview.Value(); ok {
		b := recordShutdownBinding(preview)
		result.Preview = &b
	}
	return result
}
func (r updateAttemptResultRecord) attempt() (app.UpdateAttemptResult, error) {
	status, err := r.Status.status()
	if err != nil {
		return app.UpdateAttemptResult{}, app.ErrRuntimeProtocol
	}
	result := app.UpdateAttemptResult{Target: r.Target.target(), Status: status, Notice: r.Notice}
	if r.Preview != nil {
		b, e := r.Preview.binding()
		if e != nil {
			return app.UpdateAttemptResult{}, app.ErrRuntimeProtocol
		}
		result.Preview = types.Some(b.Snapshot())
	}
	if !result.Valid() {
		return app.UpdateAttemptResult{}, app.ErrRuntimeProtocol
	}
	return result, nil
}

type updateHandoffResultRecord struct {
	Target   updateTargetRecord `json:"target"`
	Decision shutdownRecord     `json:"decision"`
}
