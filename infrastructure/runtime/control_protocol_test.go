package runtime

import (
	"bytes"
	"encoding/json"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"testing"
)

func TestControlWireStrictIntentRawBytesAndSafeReceipt(t *testing.T) {
	selection := outputWireSelection()
	intent, err := domain.ControlIntentFrom(domain.ControlIntentState{RequestID: "manual-1", Kind: domain.ControlInput, ManualTerminal: true, InputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte{0x1b, 0xff, 0}
	record := recordControlRequest(app.ControlRequest{Selection: selection, Intent: intent, Data: raw})
	request, err := record.request("input")
	if err != nil || !bytes.Equal(request.Data, raw) || request.Intent.Snapshot() != intent.Snapshot() {
		t.Fatal("lossy control wire")
	}
	if _, err = record.request("stop"); err == nil {
		t.Fatal("operation/body kind mismatch accepted")
	}
	record.Intent.InputBytes = "03"
	if _, err = record.request("input"); err == nil {
		t.Fatal("noncanonical count accepted")
	}
	receipt, _ := domain.ControlReceiptFrom(domain.ControlSnapshot{Identity: selection.Identity, Intent: intent.Snapshot(), Outcome: domain.ControlUnknown, WrittenBytes: 1})
	result := recordControlResult(app.ControlResult{Selection: selection, Receipt: receipt.Snapshot()})
	wire, err := json.Marshal(runtimeResponse{Version: RuntimeProtocolVersion, ID: "reply", Code: "control_unknown", Control: result})
	if err != nil {
		t.Fatal(err)
	}
	var reply runtimeResponse
	if err = decodeRuntimeJSON(wire, &reply); err != nil {
		t.Fatal(err)
	}
	decoded, err := reply.Control.result()
	if err != nil || decoded.Receipt.WrittenBytes != 1 || decoded.Selection != selection {
		t.Fatal("partial receipt lost")
	}
	if bytes.Contains(wire, []byte(`"data"`)) || bytes.Contains(wire, []byte("null")) {
		t.Fatal("receipt contains input or null")
	}
}
