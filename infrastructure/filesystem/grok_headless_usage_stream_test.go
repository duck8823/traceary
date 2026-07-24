package filesystem_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestGrokHeadlessUsageStream_UsesVersionedTerminalFixture(t *testing.T) {
	fixture, err := os.ReadFile("../../presentation/cli/testdata/grok_usage/v0.2.106/headless_stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	output := &bytes.Buffer{}
	stream := filesystem.NewGrokHeadlessUsageStreamFactory().New(output)
	for _, chunk := range [][]byte{fixture[:13], fixture[13:87], fixture[87:]} {
		if _, err := stream.Write(chunk); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	result, err := stream.Complete()
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !bytes.Equal(output.Bytes(), fixture) {
		t.Fatal("forwarded output changed")
	}
	if !result.BoundaryObserved || len(result.Samples) != 1 {
		t.Fatalf("result = %+v", result)
	}
	sample := result.Samples[0]
	if sample.ObservedAt.Before(before) || sample.ObservedAt.After(after) ||
		sample.ObservedAt.Location() != time.UTC {
		t.Fatalf("sample observed_at = %s, want UTC ingestion time in [%s, %s]", sample.ObservedAt, before, after)
	}
	if !sample.Available ||
		sample.RecordID != "headless_stream:ae531a76b3e09738546f60434a97e5090e00833f09aeb76ff382b98bc7d5911a" ||
		sample.Model != "grok-4.5-build" || sample.TerminalCode != types.UsageTerminalSuccess {
		t.Fatalf("sample = %+v", sample)
	}
	for name, got := range map[string]*int64{
		"input": sample.Counters.InputTokens, "cached": sample.Counters.CachedInputTokens,
		"output": sample.Counters.OutputTokens, "reasoning": sample.Counters.ReasoningTokens,
		"total": sample.Counters.TotalTokens,
	} {
		if got == nil {
			t.Fatalf("%s counter is unavailable", name)
		}
	}
	if *sample.Counters.InputTokens != 21440 || *sample.Counters.CachedInputTokens != 0 ||
		*sample.Counters.OutputTokens != 25 || *sample.Counters.ReasoningTokens != 20 ||
		*sample.Counters.TotalTokens != 21465 {
		t.Fatalf("counters = %+v", sample.Counters)
	}
	if strings.Contains(sample.RecordID, "synthetic body") {
		t.Fatalf("body entered identity: %q", sample.RecordID)
	}
}

func TestGrokHeadlessUsageStream_NormalizesMaximumProviderIdentityToBoundedRecordID(t *testing.T) {
	requestID := strings.Repeat("r", 512)
	sessionID := strings.Repeat("s", 512)
	fixture := `{"type":"end","requestId":"` + requestID + `","sessionId":"` + sessionID +
		`","stopReason":"EndTurn","num_turns":1,"usage":{"input_tokens":2,"cache_read_input_tokens":0,"output_tokens":1,"reasoning_tokens":0,"total_tokens":3}}` + "\n"
	stream := filesystem.NewGrokHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil || len(result.Samples) != 1 {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}
	recordID := result.Samples[0].RecordID
	if !strings.HasPrefix(recordID, "headless_stream:") || len(recordID) != len("headless_stream:")+64 {
		t.Fatalf("bounded record ID = %q (length %d)", recordID, len(recordID))
	}
}

func TestGrokHeadlessUsageStream_TerminalWithoutUsagePreservesProviderIdentity(t *testing.T) {
	stream := filesystem.NewGrokHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"end","requestId":"request-1","sessionId":"session-1","stopReason":"Error","num_turns":1,"error":{"message":"PRIVATE"}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !result.BoundaryObserved || len(result.Samples) != 0 ||
		result.TerminalRecordID != "headless_stream:6141b7faf78062b03ca6768a5551d1c80812d8a669d5713fe22a6310ef6cc64c" ||
		result.TerminalCode != types.UsageTerminalUnknown {
		t.Fatalf("result = %+v", result)
	}
}

func TestGrokHeadlessUsageStream_DeduplicatesNormalizedTerminalAndRejectsConflict(t *testing.T) {
	first := `{"type":"end","requestId":"request-1","sessionId":"session-1","stopReason":"EndTurn","num_turns":1,"usage":{"input_tokens":2,"cache_read_input_tokens":0,"output_tokens":1,"reasoning_tokens":0,"total_tokens":3},"modelUsage":{"model-1":{"private":"FIRST"}},"text":"PRIVATE FIRST"}` + "\n"
	equivalent := `{"text":"PRIVATE SECOND","modelUsage":{"model-1":{"private":"SECOND"}},"usage":{"reasoning_tokens":0,"total_tokens":3,"output_tokens":1,"input_tokens":2,"cache_read_input_tokens":0},"num_turns":1,"stopReason":"EndTurn","sessionId":"session-1","requestId":"request-1","type":"end"}` + "\n"
	stream := filesystem.NewGrokHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(first + equivalent)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil || !result.BoundaryObserved || len(result.Samples) != 1 {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}

	conflict := strings.Replace(first, `"total_tokens":3`, `"total_tokens":4`, 1)
	stream = filesystem.NewGrokHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(first + conflict)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err = stream.Complete()
	if err == nil || result.BoundaryObserved || len(result.Samples) != 0 {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}
}

func TestGrokHeadlessUsageStream_RejectsMalformedTerminalAndDiscardsPartialResult(t *testing.T) {
	valid := `{"type":"end","requestId":"request-1","sessionId":"session-1","stopReason":"EndTurn","num_turns":1,"usage":{"input_tokens":2,"cache_read_input_tokens":0,"output_tokens":1,"reasoning_tokens":0,"total_tokens":3},"modelUsage":{}}` + "\n"
	for name, invalid := range map[string]string{
		"negative":       `{"type":"end","requestId":"request-2","sessionId":"session-1","stopReason":"EndTurn","num_turns":1,"usage":{"input_tokens":-1,"cache_read_input_tokens":0,"output_tokens":1,"reasoning_tokens":0,"total_tokens":0}}` + "\n",
		"incomplete":     `{"type":"end","requestId":"request-2","sessionId":"session-1","stopReason":"EndTurn","num_turns":1,"usage":{"input_tokens":1}}` + "\n",
		"malformed JSON": `{"type":"end"` + "\n",
		"oversized line": strings.Repeat("x", 8*1024*1024+1) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			stream := filesystem.NewGrokHeadlessUsageStreamFactory().New(&bytes.Buffer{})
			if _, err := stream.Write([]byte(valid + invalid)); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			result, err := stream.Complete()
			if err == nil || result.BoundaryObserved || len(result.Samples) != 0 {
				t.Fatalf("Complete() = (%+v, %v)", result, err)
			}
		})
	}
}

func TestGrokHeadlessUsageStream_LeavesModelUnknownWhenAggregateSpansModels(t *testing.T) {
	stream := filesystem.NewGrokHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"end","requestId":"request-1","sessionId":"session-1","stopReason":"EndTurn","num_turns":2,"usage":{"input_tokens":2,"cache_read_input_tokens":0,"output_tokens":1,"reasoning_tokens":0,"total_tokens":3},"modelUsage":{"model-a":{},"model-b":{}}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil || len(result.Samples) != 1 || result.Samples[0].Model != "" {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}
}
