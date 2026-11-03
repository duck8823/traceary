package runtime

import (
	"encoding/base64"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type controlIntentRecord struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Manual     bool   `json:"manual"`
	WorkLoss   bool   `json:"work_loss"`
	InputBytes string `json:"input_bytes"`
	Sequence   string `json:"sequence"`
	Rows       uint16 `json:"rows"`
	Cols       uint16 `json:"cols"`
}
type controlRequestRecord struct {
	Selection outputSelectionRecord `json:"selection"`
	Intent    controlIntentRecord   `json:"intent"`
	Data      string                `json:"data,omitempty"`
}
type controlReceiptRequestRecord struct {
	Selection outputSelectionRecord `json:"selection"`
	ID        string                `json:"id"`
}
type controlResultRecord struct {
	Selection outputSelectionRecord `json:"selection"`
	Receipt   controlReceiptRecord  `json:"receipt"`
}

func recordControlRequest(request app.ControlRequest) *controlRequestRecord {
	i := request.Intent.Snapshot()
	return &controlRequestRecord{Selection: recordSelection(request.Selection), Intent: controlIntentRecord{ID: i.RequestID, Kind: string(i.Kind), Manual: i.ManualTerminal, WorkLoss: i.WorkLossAcknowledged, InputBytes: domain.ByteOffset(i.InputBytes).String(), Sequence: domain.ByteOffset(i.ResizeSequence).String(), Rows: i.Rows, Cols: i.Cols}, Data: base64.StdEncoding.EncodeToString(request.Data)}
}
func (r controlRequestRecord) request(operation string) (app.ControlRequest, error) {
	selection, err := r.Selection.selection()
	if err != nil {
		return app.ControlRequest{}, app.ErrRuntimeProtocol
	}
	count, e1 := domain.ParseByteOffset(r.Intent.InputBytes)
	sequence, e2 := domain.ParseByteOffset(r.Intent.Sequence)
	if e1 != nil || e2 != nil || count > domain.MaxControlInputBytes || r.Intent.Kind != operation {
		return app.ControlRequest{}, app.ErrRuntimeProtocol
	}
	intent, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: r.Intent.ID, Kind: domain.ControlKind(r.Intent.Kind), ManualTerminal: r.Intent.Manual, WorkLossAcknowledged: r.Intent.WorkLoss, InputBytes: int(count), ResizeSequence: uint64(sequence), Rows: r.Intent.Rows, Cols: r.Intent.Cols})
	if err != nil || len(r.Data) != base64.StdEncoding.EncodedLen(int(count)) {
		return app.ControlRequest{}, app.ErrRuntimeProtocol
	}
	data, err := base64.StdEncoding.Strict().DecodeString(r.Data)
	if err != nil || len(data) != int(count) || base64.StdEncoding.EncodeToString(data) != r.Data {
		clear(data)
		return app.ControlRequest{}, app.ErrRuntimeProtocol
	}
	return app.ControlRequest{Selection: selection, Intent: intent, Data: data}, nil
}
func recordControlResult(result app.ControlResult) *controlResultRecord {
	receipt, err := domain.ControlReceiptFrom(result.Receipt)
	if err != nil {
		return nil
	}
	return &controlResultRecord{Selection: recordSelection(result.Selection), Receipt: recordControl(receipt)}
}
func (r controlResultRecord) result() (app.ControlResult, error) {
	selection, err := r.Selection.selection()
	if err != nil {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	receipt, err := r.Receipt.receipt(selection)
	if err != nil {
		return app.ControlResult{}, app.ErrRuntimeProtocol
	}
	return app.ControlResult{Selection: selection, Receipt: receipt.Snapshot()}, nil
}
