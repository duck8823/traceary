package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// This DTO cannot hold raw input, HMAC fingerprints, environment or process IDs.
type controlReceiptRecord struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Manual       bool   `json:"manual"`
	WorkLoss     bool   `json:"work_loss"`
	InputBytes   string `json:"input_bytes"`
	Sequence     string `json:"sequence"`
	Rows         uint16 `json:"rows"`
	Cols         uint16 `json:"cols"`
	Outcome      string `json:"outcome"`
	WrittenBytes string `json:"written_bytes"`
}

func recordControl(r domain.ControlReceipt) controlReceiptRecord {
	s := r.Snapshot()
	i := s.Intent
	return controlReceiptRecord{ID: i.RequestID, Kind: string(i.Kind), Manual: i.ManualTerminal, WorkLoss: i.WorkLossAcknowledged, InputBytes: domain.ByteOffset(i.InputBytes).String(), Sequence: domain.ByteOffset(i.ResizeSequence).String(), Rows: i.Rows, Cols: i.Cols, Outcome: string(s.Outcome), WrittenBytes: domain.ByteOffset(s.WrittenBytes).String()}
}
func (r controlReceiptRecord) receipt(selection app.OutputSelection) (domain.ControlReceipt, error) {
	count, e1 := domain.ParseByteOffset(r.InputBytes)
	sequence, e2 := domain.ParseByteOffset(r.Sequence)
	written, e3 := domain.ParseByteOffset(r.WrittenBytes)
	if e1 != nil || e2 != nil || e3 != nil || count > domain.MaxControlInputBytes || written > count {
		return domain.ControlReceipt{}, domain.ErrInvalid
	}
	receipt, err := domain.ControlReceiptFrom(domain.ControlSnapshot{Identity: selection.Identity, Intent: domain.ControlIntentState{RequestID: r.ID, Kind: domain.ControlKind(r.Kind), ManualTerminal: r.Manual, WorkLossAcknowledged: r.WorkLoss, InputBytes: int(count), ResizeSequence: uint64(sequence), Rows: r.Rows, Cols: r.Cols}, Outcome: domain.ControlOutcome(r.Outcome), WrittenBytes: int(written)})
	if err != nil {
		return domain.ControlReceipt{}, domain.ErrInvalid
	}
	return receipt, nil
}
