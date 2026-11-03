package runtime

import (
	"bytes"
	"encoding/json"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"testing"
)

func TestOutputWireExactCountersAndUntrustedBytes(t *testing.T) {
	selection := outputWireSelection()
	frame := app.OutputFrame{Kind: "data", Info: app.OutputInfo{Selection: selection, Head: 9007199254740995, Base: 9007199254740993, Resize: app.ResizeEvidence{Rows: 24, Cols: 80}}, Start: 9007199254740993, End: 9007199254740995, Data: []byte{0x1b, 0xff}}
	record := recordOutput(frame)
	wire, err := json.Marshal(runtimeResponse{Version: RuntimeProtocolVersion, ID: "test", Code: "ok", Output: record})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("null")) || bytes.Contains(wire, []byte{0x1b}) {
		t.Fatal("unsafe wire encoding")
	}
	var reply runtimeResponse
	if err = decodeRuntimeJSON(wire, &reply); err != nil {
		t.Fatal(err)
	}
	got, err := reply.Output.frame()
	if err != nil || got.Start != frame.Start || !bytes.Equal(got.Data, frame.Data) {
		t.Fatal("lossy wire output")
	}
	record.Start = "09007199254740993"
	if _, err = record.frame(); err == nil {
		t.Fatal("noncanonical counter accepted")
	}
}

func outputWireSelection() app.OutputSelection {
	return app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: "11111111111111111111111111111111", Run: "22222222222222222222222222222222", Generation: "33333333333333333333333333333333"}, Worktree: domain.WorktreeID("w_" + string(bytes.Repeat([]byte("a"), 64))), Profile: "test"}
}
