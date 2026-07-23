package filesystem_test

import (
	"bytes"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestCodexHeadlessUsageStream_ForwardsBodyButRetainsOnlyTerminalUsage(t *testing.T) {
	output := &bytes.Buffer{}
	stream := filesystem.NewCodexHeadlessUsageStreamFactory().New(output)
	fixture := "" +
		`{"type":"thread.started","thread_id":"thread-1","private":"ignored"}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"private body"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":21,"cached_input_tokens":10,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":2}}` + "\n"
	for _, chunk := range [][]byte{[]byte(fixture[:17]), []byte(fixture[17:63]), []byte(fixture[63:])} {
		if _, err := stream.Write(chunk); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	result, err := stream.Complete()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if output.String() != fixture {
		t.Fatalf("forwarded output changed")
	}
	if len(result.Samples) != 1 || !result.BoundaryObserved {
		t.Fatalf("result = %+v", result)
	}
	sample := result.Samples[0]
	if sample.RecordID != "headless_stream:thread-1:1" || !sample.Available || sample.TerminalCode != types.UsageTerminalSuccess {
		t.Fatalf("sample = %+v", sample)
	}
	if sample.Model != "" || sample.Counters.TotalTokens != nil {
		t.Fatalf("unreported fields were inferred: %+v", sample)
	}
}

func TestCodexHeadlessUsageStream_RejectsMalformedTerminalUsageWithoutEchoingItInError(t *testing.T) {
	stream := filesystem.NewCodexHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"thread.started","thread_id":"thread-1"}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":"private-invalid"}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err := stream.Complete()
	if err == nil || bytes.Contains([]byte(err.Error()), []byte("private-invalid")) {
		t.Fatalf("Complete() error = %v", err)
	}
}
