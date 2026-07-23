package filesystem_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestGeminiHeadlessUsageStream_UsesVersionedTerminalFixtureWithoutDoubleCountingAggregate(t *testing.T) {
	fixture, err := os.ReadFile("../../presentation/cli/testdata/gemini_usage/v0.46.0/headless_stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(output)
	for _, chunk := range [][]byte{fixture[:17], fixture[17:91], fixture[91:]} {
		if _, err := stream.Write(chunk); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	result, err := stream.Complete()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !bytes.Equal(output.Bytes(), fixture) {
		t.Fatal("forwarded output changed")
	}
	if !result.BoundaryObserved || len(result.Samples) != 2 {
		t.Fatalf("result = %+v", result)
	}
	var totalInput, totalOutput int64
	for _, sample := range result.Samples {
		if !sample.Available || sample.TerminalCode != types.UsageTerminalSuccess ||
			sample.Counters.InputTokens == nil || sample.Counters.OutputTokens == nil {
			t.Fatalf("sample = %+v", sample)
		}
		totalInput += *sample.Counters.InputTokens
		totalOutput += *sample.Counters.OutputTokens
		if strings.Contains(sample.RecordID, "ignored synthetic body") {
			t.Fatalf("body entered identity: %q", sample.RecordID)
		}
	}
	if totalInput != 21 || totalOutput != 9 {
		t.Fatalf("model totals = input %d output %d", totalInput, totalOutput)
	}
}

func TestGeminiHeadlessUsageStream_TerminalWithoutStatsLeavesUnavailableFallbackOpen(t *testing.T) {
	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-1","model":"model-1"}` + "\n" +
		`{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"error","error":{"message":"PRIVATE"}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.BoundaryObserved || len(result.Samples) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestGeminiHeadlessUsageStream_PreservesKnownZeroCounters(t *testing.T) {
	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-zero","model":"model-zero"}` + "\n" +
		`{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":0,"input_tokens":0,"output_tokens":0,"cached":0,"input":0,"duration_ms":0,"tool_calls":0,"models":{}}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil || len(result.Samples) != 1 {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}
	sample := result.Samples[0]
	for name, value := range map[string]*int64{
		"input": sample.Counters.InputTokens, "cached": sample.Counters.CachedInputTokens,
		"output": sample.Counters.OutputTokens, "total": sample.Counters.TotalTokens,
	} {
		if value == nil || *value != 0 {
			t.Fatalf("%s counter = %v, want known zero", name, value)
		}
	}
}

func TestGeminiHeadlessUsageStream_DeduplicatesExactTerminalAndRejectsConflict(t *testing.T) {
	init := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-1","model":"model-1"}` + "\n"
	result := `{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1,"duration_ms":1,"tool_calls":0,"models":{"model-1":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1}}}}` + "\n"
	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(init + result + result)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	loaded, err := stream.Complete()
	if err != nil || len(loaded.Samples) != 1 {
		t.Fatalf("Complete() = (%+v, %v)", loaded, err)
	}

	conflict := strings.Replace(result, `"output_tokens":1`, `"output_tokens":2`, 1)
	stream = filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(init + result + conflict)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	loaded, err = stream.Complete()
	if err == nil || loaded.BoundaryObserved || len(loaded.Samples) != 0 {
		t.Fatalf("Complete() = (%+v, %v)", loaded, err)
	}
}

func TestGeminiHeadlessUsageStream_RejectsConflictingTerminalWithoutStats(t *testing.T) {
	init := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-1","model":"model-1"}` + "\n"
	noStatsError := `{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"error","error":{"message":"PRIVATE"}}` + "\n"
	withStatsSuccess := `{"type":"result","timestamp":"2026-07-23T01:00:02Z","status":"success","stats":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1,"models":{}}}` + "\n"
	noStatsSuccess := `{"type":"result","timestamp":"2026-07-23T01:00:03Z","status":"success"}` + "\n"

	for name, terminalEvents := range map[string]string{
		"error without stats then usage":   noStatsError + withStatsSuccess,
		"success without stats then usage": noStatsSuccess + withStatsSuccess,
		"different results without stats":  noStatsError + noStatsSuccess,
	} {
		t.Run(name, func(t *testing.T) {
			stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
			if _, err := stream.Write([]byte(init + terminalEvents)); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			result, err := stream.Complete()
			if err == nil || result.BoundaryObserved || len(result.Samples) != 0 {
				t.Fatalf("Complete() = (%+v, %v)", result, err)
			}
		})
	}

	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	noStatsErrorWithDifferentPrivateBody := strings.Replace(noStatsError, "PRIVATE", "OTHER PRIVATE", 1)
	if _, err := stream.Write([]byte(init + noStatsError + noStatsErrorWithDifferentPrivateBody)); err != nil {
		t.Fatalf("Write() exact duplicate error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil || result.BoundaryObserved || len(result.Samples) != 0 {
		t.Fatalf("Complete() exact duplicate = (%+v, %v)", result, err)
	}
}

func TestGeminiHeadlessUsageStream_RejectsInputBreakdownOverflow(t *testing.T) {
	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-overflow","model":"model-1"}` + "\n" +
		`{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":1,"input_tokens":1,"output_tokens":0,"cached":1,"input":9223372036854775807,"models":{}}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err == nil || result.BoundaryObserved || len(result.Samples) != 0 {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}
}

func TestGeminiHeadlessUsageStream_NormalizesTerminalMetadataSignature(t *testing.T) {
	init := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-normalized","model":"model-1"}` + "\n"
	withoutModels := `{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1,"private":"FIRST"},"error":{"message":"PRIVATE FIRST"}}` + "\n"
	withEmptyModels := `{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1,"models":{},"private":"SECOND"},"error":{"message":"PRIVATE SECOND"}}` + "\n"
	changedCounters := `{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":4,"input_tokens":2,"output_tokens":2,"cached":1,"input":1,"models":{}}}` + "\n"

	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(init + withoutModels + withEmptyModels)); err != nil {
		t.Fatalf("Write() normalized redelivery error = %v", err)
	}
	result, err := stream.Complete()
	if err != nil || !result.BoundaryObserved || len(result.Samples) != 1 {
		t.Fatalf("Complete() normalized redelivery = (%+v, %v)", result, err)
	}

	stream = filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	if _, err := stream.Write([]byte(init + withEmptyModels + changedCounters)); err != nil {
		t.Fatalf("Write() changed counters error = %v", err)
	}
	result, err = stream.Complete()
	if err == nil || result.BoundaryObserved || len(result.Samples) != 0 {
		t.Fatalf("Complete() changed counters = (%+v, %v)", result, err)
	}
}

func TestGeminiHeadlessUsageStream_RejectsModelCounterOverflow(t *testing.T) {
	stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
	fixture := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-overflow","model":"model-1"}` + "\n" +
		`{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":1,"input_tokens":0,"output_tokens":0,"cached":0,"input":0,"models":{"model-1":{"total_tokens":9223372036854775807,"input_tokens":0,"output_tokens":0,"cached":0,"input":0},"model-2":{"total_tokens":9223372036854775807,"input_tokens":0,"output_tokens":0,"cached":0,"input":0},"model-3":{"total_tokens":3,"input_tokens":0,"output_tokens":0,"cached":0,"input":0}}}}` + "\n"
	if _, err := stream.Write([]byte(fixture)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	result, err := stream.Complete()
	if err == nil || result.BoundaryObserved || len(result.Samples) != 0 {
		t.Fatalf("Complete() = (%+v, %v)", result, err)
	}
}

func TestGeminiHeadlessUsageStream_DiscardsPartialTerminalAfterParseFailure(t *testing.T) {
	valid := `{"type":"init","timestamp":"2026-07-23T01:00:00Z","session_id":"session-1","model":"model-1"}` + "\n" +
		`{"type":"result","timestamp":"2026-07-23T01:00:01Z","status":"success","stats":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1,"duration_ms":1,"tool_calls":0,"models":{}}}` + "\n"
	for name, invalid := range map[string]string{
		"malformed JSON":    `{"type":"result"` + "\n",
		"mismatched totals": `{"type":"result","timestamp":"2026-07-23T01:00:02Z","status":"success","stats":{"total_tokens":4,"input_tokens":2,"output_tokens":1,"cached":1,"input":1,"duration_ms":1,"tool_calls":0,"models":{"model-1":{"total_tokens":3,"input_tokens":2,"output_tokens":1,"cached":1,"input":1}}}}` + "\n",
		"oversized line":    strings.Repeat("x", 8*1024*1024+1) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			stream := filesystem.NewGeminiHeadlessUsageStreamFactory().New(&bytes.Buffer{})
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
