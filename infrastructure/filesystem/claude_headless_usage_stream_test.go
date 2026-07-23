package filesystem_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestClaudeHeadlessUsageStream_ForwardsBodiesButRetainsOnlyTerminalResult(t *testing.T) {
	output := &bytes.Buffer{}
	stream := filesystem.NewClaudeHeadlessUsageStreamFactory().New(output)
	fixture := "" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"PRIVATE-BODY"}],"usage":{"input_tokens":999,"output_tokens":999}}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"claude-session-1","result":"PRIVATE-RESULT","usage":{"input_tokens":21,"cache_creation_input_tokens":0,"cache_read_input_tokens":10,"output_tokens":5},"modelUsage":{"claude-opus-4-1":{"costUSD":0.1}}}` + "\n"
	for _, chunk := range [][]byte{[]byte(fixture[:17]), []byte(fixture[17:83]), []byte(fixture[83:])} {
		if _, err := stream.Write(chunk); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	result, err := stream.Complete()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if output.String() != fixture {
		t.Fatal("forwarded output changed")
	}
	if result.Mode != application.ClaudeUsageModeOneShotStream ||
		!result.BoundaryObserved || len(result.Samples) != 1 {
		t.Fatalf("result = %+v", result)
	}
	sample := result.Samples[0]
	if sample.Scope != types.UsageScopeRun || !sample.Available ||
		sample.Model != "claude-opus-4-1" ||
		sample.Counters.InputTokens == nil || *sample.Counters.InputTokens != 21 {
		t.Fatalf("sample = %+v", sample)
	}
	if strings.Contains(sample.RecordID, "PRIVATE") {
		t.Fatalf("private body entered identity: %q", sample.RecordID)
	}
}

func TestClaudeHeadlessUsageStream_MissingTerminalUsageRemainsUnavailable(t *testing.T) {
	stream := filesystem.NewClaudeHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"result","subtype":"error","is_error":true,"session_id":"claude-session-1","error":"PRIVATE-FAILURE"}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Samples) != 1 || result.Samples[0].Available ||
		result.Samples[0].TerminalCode != types.UsageTerminalFailure {
		t.Fatalf("result = %+v", result)
	}
}

func TestClaudeHeadlessUsageStream_RejectsMalformedUsageWithoutEchoingBody(t *testing.T) {
	stream := filesystem.NewClaudeHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"result","session_id":"claude-session-1","usage":{"input_tokens":"PRIVATE-INVALID"}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	_, err := stream.Complete()
	if err == nil || strings.Contains(err.Error(), "PRIVATE-INVALID") {
		t.Fatalf("Complete() error = %v", err)
	}
}
