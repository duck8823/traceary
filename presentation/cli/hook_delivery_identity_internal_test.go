package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCrossHostHookFixturesExposeStableDeliveryIdentity(t *testing.T) {
	t.Parallel()

	readFixture := func(path string) []byte {
		t.Helper()
		payload, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		return payload
	}

	tests := []struct {
		name       string
		normalized func() []byte
		sourceHook string
		want       string
	}{
		{
			name: "codex event identity",
			normalized: func() []byte {
				return []byte(`{"session_id":"codex-session","event_id":"codex-event-1","cwd":"/repo"}`)
			},
			sourceHook: "stop",
			want:       "event_id:codex-event-1",
		},
		{
			name: "claude tool-use identity",
			normalized: func() []byte {
				return []byte(`{"session_id":"claude-session","tool_use_id":"toolu_1","cwd":"/repo"}`)
			},
			sourceHook: "post_tool_use",
			want:       "tool_use_id:toolu_1",
		},
		{
			name: "grok camel-case tool identity",
			normalized: func() []byte {
				payload, err := normalizeGrokHookPayload(bytes.NewReader(readFixture("testdata/grok_hooks/v0.2.99/post_tool_use.json")))
				if err != nil {
					t.Fatalf("normalizeGrokHookPayload() error = %v", err)
				}
				return payload
			},
			sourceHook: "post_tool_use",
			want:       "tool_use_id:tool-contract-probe-1",
		},
		{
			name: "kimi tool-call identity",
			normalized: func() []byte {
				payload, err := normalizeKimiHookPayload(bytes.NewReader(readFixture("testdata/kimi_hooks/v0.27.0/post_tool_use.json")))
				if err != nil {
					t.Fatalf("normalizeKimiHookPayload() error = %v", err)
				}
				return payload
			},
			sourceHook: "post_tool_use",
			want:       "tool_use_id:tool_0000000000000000000000AA",
		},
		{
			name: "antigravity step identity",
			normalized: func() []byte {
				return normalizeAntigravityPayload(antigravityNormalizeOptions{
					sessionID: "conversation-1",
					cwd:       "/repo",
					toolUseID: "step:7",
				})
			},
			sourceHook: "post_tool_use",
			want:       "tool_use_id:step:7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := tt.normalized()
			second := tt.normalized()
			if got := resolveHookDeliveryNativeID(first, tt.sourceHook); got != tt.want {
				t.Fatalf("resolveHookDeliveryNativeID() = %q, want %q", got, tt.want)
			}
			if left, right := resolveHookDeliveryNativeID(first, tt.sourceHook), resolveHookDeliveryNativeID(second, tt.sourceHook); left != right {
				t.Fatalf("fixture replay identity changed: %q != %q", left, right)
			}
		})
	}
}

func TestGeminiHookWithoutProvenNativeIDStaysDistinct(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"session_id":"gemini-session","tool_name":"run_shell_command","cwd":"/repo"}`)
	if got := resolveHookDeliveryNativeID(payload, "after_tool"); got != "" {
		t.Fatalf("resolveHookDeliveryNativeID() = %q, want empty for Gemini payload without a proven native ID", got)
	}
}

func TestResolveHookDeliveryNativeID_NeverFallsBackToContent(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"prompt":"same prompt","cwd":"/repo"}`)
	if got := resolveHookDeliveryNativeID(payload, "user_prompt_submit"); got != "" {
		t.Fatalf("resolveHookDeliveryNativeID() = %q, want empty without host-native evidence", got)
	}
}
