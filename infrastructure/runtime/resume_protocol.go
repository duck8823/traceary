package runtime

import (
	"bytes"
	"encoding/json"
	"io"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type resumeRequestRecord struct {
	Epoch     string                `json:"epoch"`
	RequestID string                `json:"request_id"`
	Selection outputSelectionRecord `json:"selection"`
	WorkLoss  *bool                 `json:"work_loss"`
}
type resumeResultRecord struct {
	Epoch     string                 `json:"epoch"`
	RequestID string                 `json:"request_id"`
	Previous  outputSelectionRecord  `json:"previous_selection"`
	Selection *outputSelectionRecord `json:"selection,omitempty"`
	Phase     string                 `json:"phase"`
	Owned     *bool                  `json:"owned"`
}

func recordResumeRequest(r app.ResumeRequest) *resumeRequestRecord {
	return &resumeRequestRecord{Epoch: r.Epoch, RequestID: r.RequestID, Selection: recordSelection(r.Selection), WorkLoss: &r.WorkLossAcknowledged}
}
func (r resumeRequestRecord) request() (app.ResumeRequest, error) {
	selection, err := r.Selection.selection()
	if err != nil || r.WorkLoss == nil {
		return app.ResumeRequest{}, app.ErrRuntimeProtocol
	}
	result := app.ResumeRequest{Epoch: r.Epoch, Selection: selection, RequestID: r.RequestID, WorkLossAcknowledged: *r.WorkLoss}
	if !result.Valid() {
		return app.ResumeRequest{}, app.ErrRuntimeProtocol
	}
	return result, nil
}
func recordResumeResult(request app.ResumeRequest, result app.ResumeResult) *resumeResultRecord {
	if result.Phase == "" {
		return nil
	}
	out := &resumeResultRecord{Epoch: request.Epoch, RequestID: request.RequestID, Previous: recordSelection(request.Selection), Phase: string(result.Phase), Owned: &result.Owned}
	if result.Selection.Identity.Valid() {
		selection := recordSelection(result.Selection)
		out.Selection = &selection
	}
	return out
}
func (r resumeResultRecord) result(request app.ResumeRequest) (app.ResumeResult, error) {
	previous, err := r.Previous.selection()
	if err != nil || r.Epoch != request.Epoch || r.RequestID != request.RequestID || previous != request.Selection || r.Owned == nil {
		return app.ResumeResult{}, app.ErrRuntimeProtocol
	}
	result := app.ResumeResult{Phase: domain.ResumePhase(r.Phase), Owned: *r.Owned}
	switch result.Phase {
	case domain.ResumeReserved, domain.ResumeStarted, domain.ResumeConfirmed, domain.ResumeUnknown, domain.ResumeFailedBeforeStart:
	default:
		return app.ResumeResult{}, app.ErrRuntimeProtocol
	}
	if r.Selection == nil {
		if result.Phase != domain.ResumeUnknown || result.Owned {
			return app.ResumeResult{}, app.ErrRuntimeProtocol
		}
		return result, nil
	}
	result.Selection, err = r.Selection.selection()
	if err != nil || result.Selection.Worktree != request.Selection.Worktree || result.Selection.Profile != request.Selection.Profile || result.Selection.Identity.Run == previous.Identity.Run || result.Selection.Identity.Generation == previous.Identity.Generation || result.Owned && result.Selection.Identity.Epoch != request.Epoch || result.Phase == domain.ResumeFailedBeforeStart && result.Owned {
		return app.ResumeResult{}, app.ErrRuntimeProtocol
	}
	return result, nil
}
func decodeResumeFields(data []byte, out any, required, optional []string) (map[string]json.RawMessage, error) {
	fields, err := selectedObjectFields(data, required, optional)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, app.ErrRuntimeProtocol
	}
	return fields, nil
}
func resumeSelectionFields(data []byte) error {
	_, err := selectedObjectFields(data, []string{"epoch", "run", "generation", "worktree", "profile"}, nil)
	return err
}
func (r *resumeRequestRecord) UnmarshalJSON(data []byte) error {
	type plain resumeRequestRecord
	var value plain
	fields, err := decodeResumeFields(data, &value, []string{"epoch", "request_id", "selection", "work_loss"}, nil)
	if err != nil {
		return err
	}
	if err = resumeSelectionFields(fields["selection"]); err != nil {
		return err
	}
	*r = resumeRequestRecord(value)
	return nil
}
func (r *resumeResultRecord) UnmarshalJSON(data []byte) error {
	type plain resumeResultRecord
	var value plain
	fields, err := decodeResumeFields(data, &value, []string{"epoch", "request_id", "previous_selection", "phase", "owned"}, []string{"selection"})
	if err != nil {
		return err
	}
	if err = resumeSelectionFields(fields["previous_selection"]); err != nil {
		return err
	}
	if selection, ok := fields["selection"]; ok {
		if err = resumeSelectionFields(selection); err != nil {
			return err
		}
	}
	*r = resumeResultRecord(value)
	return nil
}
