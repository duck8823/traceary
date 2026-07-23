package filesystem_test

import (
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestAntigravityUsageSource_DecodesOnlyIdleCumulativeTotals(t *testing.T) {
	source := filesystem.NewAntigravityUsageSource()
	private := "private@example.com"
	payload := `{
	  "session_id":"conversation-1",
	  "conversation_id":"conversation-1",
	  "version":"1.1.5",
	  "product":"antigravity",
	  "agent_state":"idle",
	  "model":{"id":"Gemini 3.5 Flash (High)","display_name":"ignored"},
	  "context_window":{
	    "total_input_tokens":88244,
	    "total_output_tokens":61074,
	    "current_usage":{"input_tokens":63382,"output_tokens":346,"cache_read_input_tokens":20857}
	  },
	  "email":"` + private + `",
	  "quota":{"private":{"remaining_fraction":0.9}}
	}`
	snapshot, err := source.Decode(context.Background(), strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if snapshot == nil || snapshot.ConversationID != "conversation-1" ||
		snapshot.Model != "Gemini 3.5 Flash (High)" ||
		snapshot.SourceVersion != "1.1.5" ||
		snapshot.InputTokens != 88244 || snapshot.OutputTokens != 61074 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestAntigravityUsageSource_IgnoresNonIdleAndMissingTotals(t *testing.T) {
	source := filesystem.NewAntigravityUsageSource()
	for name, payload := range map[string]string{
		"working":        `{"agent_state":"working"}`,
		"missing totals": `{"agent_state":"idle","conversation_id":"conversation-1","version":"1.1.5","model":{"id":"model-1"},"context_window":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			snapshot, err := source.Decode(context.Background(), strings.NewReader(payload))
			if err != nil || snapshot != nil {
				t.Fatalf("Decode() = (%+v, %v)", snapshot, err)
			}
		})
	}
}

func TestAntigravityUsageSource_RejectsMalformedOrOversizedWithoutEchoingBody(t *testing.T) {
	source := filesystem.NewAntigravityUsageSource()
	private := "PRIVATE-PAYLOAD"
	for name, payload := range map[string]string{
		"malformed":           `{"agent_state":"idle","email":"` + private + `"`,
		"oversized":           strings.Repeat(private, 100000),
		"control in identity": `{"agent_state":"idle","conversation_id":"conversation\u0000private","version":"1.1.5","model":{"id":"model-1"},"context_window":{"total_input_tokens":1,"total_output_tokens":1}}`,
		"long identity":       `{"agent_state":"idle","conversation_id":"` + strings.Repeat("x", 513) + `","version":"1.1.5","model":{"id":"model-1"},"context_window":{"total_input_tokens":1,"total_output_tokens":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := source.Decode(context.Background(), strings.NewReader(payload))
			if err == nil || strings.Contains(err.Error(), private) {
				t.Fatalf("Decode() error = %v", err)
			}
		})
	}
}
