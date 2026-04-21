package filesystem

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestMergeHooksDocument_ReplacesTracearyManagedHooks(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "echo custom-start"
          },
          {
            "name": "traceary-session-start",
            "type": "command",
            "command": "bash '/old/scripts/traceary-session.sh' 'gemini' 'start'"
          }
        ]
      }
    ]
  }
}`)

	desired := (&GeminiHooksHandler{}).Build("traceary")

	merged, err := mergeHooksDocument(existing, desired)
	if err != nil {
		t.Fatalf("mergeHooksDocument() error = %v", err)
	}

	if !strings.Contains(string(merged), `"theme": "dark"`) {
		t.Fatalf("merged output lost unrelated top-level field: %s", merged)
	}
	if !strings.Contains(string(merged), `"command": "echo custom-start"`) {
		t.Fatalf("merged output lost custom command: %s", merged)
	}
	if strings.Count(string(merged), "traceary-session-start") != 1 {
		t.Fatalf("merged output should contain exactly one traceary-session-start entry: %s", merged)
	}
	if strings.Contains(string(merged), "/old/scripts") {
		t.Fatalf("merged output kept old script path: %s", merged)
	}
	if !strings.Contains(string(merged), `'traceary' 'hook' 'session' 'gemini' 'start'`) {
		t.Fatalf("merged output missing direct hook runtime command: %s", merged)
	}
}

func TestMergeHooksDocument_RemovesSessionStartCompactVariant(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "compact",
        "hooks": [
          {
            "type": "command",
            "command": "bash '/old/scripts/traceary-compact.sh' 'claude' 'session-start-compact'"
          }
        ]
      }
    ]
  }
}`)

	desired := (&ClaudeHooksHandler{}).Build("traceary")

	merged, err := mergeHooksDocument(existing, desired)
	if err != nil {
		t.Fatalf("mergeHooksDocument() error = %v", err)
	}

	occurrences := strings.Count(string(merged), "session-start-compact")
	if occurrences != 1 {
		t.Fatalf("merged output should contain exactly one session-start-compact entry, got %d: %s", occurrences, merged)
	}
	if strings.Contains(string(merged), "/old/scripts") {
		t.Fatalf("merged output kept old script path: %s", merged)
	}
}

func TestMergeHooksDocument_RemovesCustomWrapperDirectHooksWithoutTouchingOtherCommands(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "'custom-cli' 'hook' 'session' 'claude' 'start'"
          },
          {
            "type": "command",
            "command": "'/tmp/custom-traceary-wrapper' 'hook' 'session' 'claude' 'start'"
          }
        ]
      }
    ]
  }
}`)

	desired := (&ClaudeHooksHandler{}).Build("/tmp/custom-traceary-wrapper")

	merged, err := mergeHooksDocument(existing, desired)
	if err != nil {
		t.Fatalf("mergeHooksDocument() error = %v", err)
	}

	if !strings.Contains(string(merged), `"command": "'custom-cli' 'hook' 'session' 'claude' 'start'"`) {
		t.Fatalf("merged output removed unrelated custom wrapper command: %s", merged)
	}

	if strings.Count(string(merged), `'/tmp/custom-traceary-wrapper' 'hook' 'session' 'claude' 'start'`) != 1 {
		t.Fatalf("merged output should contain exactly one managed custom-wrapper session hook: %s", merged)
	}
}

func TestMergeHooksDocument_EmptyExistingReturnsMarshaled(t *testing.T) {
	t.Parallel()

	hooks := model.HooksOf(
		[]string{"SessionStart"},
		map[string][]model.HookEntry{
			"SessionStart": {
				model.HookEntryOf(types.Some("*"), []model.HookCommand{
					model.HookCommandOf("", "command", "echo hi", types.None[int](), "", ""),
				}),
			},
		},
	)

	merged, err := mergeHooksDocument([]byte(""), hooks)
	if err != nil {
		t.Fatalf("mergeHooksDocument() error = %v", err)
	}
	var decoded hookSettingsDocument
	if err := json.Unmarshal(merged, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(decoded.Hooks["SessionStart"]) != 1 {
		t.Fatalf("merged output missing SessionStart entry: %s", merged)
	}
}

func TestMergeHooksDocument_InvalidExistingFails(t *testing.T) {
	t.Parallel()

	_, err := mergeHooksDocument([]byte("{invalid"), (&ClaudeHooksHandler{}).Build("traceary"))
	if err == nil {
		t.Fatalf("mergeHooksDocument() error = nil, want error")
	}
}

func TestMergeHooksDocumentWithDiff_EmptyExistingAddsAllEvents(t *testing.T) {
	t.Parallel()

	desired := (&ClaudeHooksHandler{}).Build("traceary")
	_, diff, err := mergeHooksDocumentWithDiff(nil, desired)
	if err != nil {
		t.Fatalf("mergeHooksDocumentWithDiff() error = %v", err)
	}
	if len(diff.AddedEvents) == 0 {
		t.Fatalf("expected all desired events to appear as Added on empty existing, got empty Added")
	}
	if len(diff.RefreshedEvents) != 0 || len(diff.PreservedEvents) != 0 {
		t.Fatalf("expected only Added events on empty existing, got refreshed=%v preserved=%v", diff.RefreshedEvents, diff.PreservedEvents)
	}
}

func TestMergeHooksDocumentWithDiff_IdenticalSetsPreserved(t *testing.T) {
	t.Parallel()

	desired := (&ClaudeHooksHandler{}).Build("traceary")
	initial, _, err := mergeHooksDocumentWithDiff(nil, desired)
	if err != nil {
		t.Fatalf("first merge error = %v", err)
	}
	merged, diff, err := mergeHooksDocumentWithDiff(initial, desired)
	if err != nil {
		t.Fatalf("re-merge error = %v", err)
	}
	if len(diff.AddedEvents) != 0 || len(diff.RefreshedEvents) != 0 {
		t.Fatalf("expected idempotent re-run, got added=%v refreshed=%v", diff.AddedEvents, diff.RefreshedEvents)
	}
	if len(diff.PreservedEvents) == 0 {
		t.Fatalf("expected preserved events on re-run, got empty")
	}
	// Byte-level idempotence: the merged output on the second run should
	// contain the same Traceary entries as the initial output. The outer
	// JSON may differ by key ordering from json.MarshalIndent of maps, so
	// we only assert the Traceary command stays intact.
	if !strings.Contains(string(merged), "'traceary' 'hook' 'session'") {
		t.Fatalf("merged output lost traceary session hook: %s", merged)
	}
}

func TestMergeHooksDocumentWithDiff_DifferentKeysRefreshed(t *testing.T) {
	t.Parallel()

	existing := []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "bash '/old/scripts/traceary-session.sh' 'claude' 'start'"
          }
        ]
      }
    ]
  }
}`)
	desired := (&ClaudeHooksHandler{}).Build("traceary")

	_, diff, err := mergeHooksDocumentWithDiff(existing, desired)
	if err != nil {
		t.Fatalf("mergeHooksDocumentWithDiff() error = %v", err)
	}
	foundRefresh := false
	for _, event := range diff.RefreshedEvents {
		if event == "SessionStart" {
			foundRefresh = true
			break
		}
	}
	if !foundRefresh {
		t.Fatalf("expected SessionStart in Refreshed, got refreshed=%v added=%v preserved=%v", diff.RefreshedEvents, diff.AddedEvents, diff.PreservedEvents)
	}
}

func TestMergeHooksDocumentWithDiff_MissingEventAdded(t *testing.T) {
	t.Parallel()

	// existing only has SessionStart entries that match the current desired
	// set (both the '*' entry and the 'compact' entry), so SessionStart
	// should be preserved and every other desired event should be Added.
	existing := []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "'traceary' 'hook' 'session' 'claude' 'start'"
          }
        ]
      },
      {
        "matcher": "compact",
        "hooks": [
          {
            "type": "command",
            "command": "'traceary' 'hook' 'compact' 'claude' 'session-start-compact'"
          }
        ]
      }
    ]
  }
}`)
	desired := (&ClaudeHooksHandler{}).Build("traceary")

	_, diff, err := mergeHooksDocumentWithDiff(existing, desired)
	if err != nil {
		t.Fatalf("mergeHooksDocumentWithDiff() error = %v", err)
	}
	foundPreservedSession := false
	for _, event := range diff.PreservedEvents {
		if event == "SessionStart" {
			foundPreservedSession = true
			break
		}
	}
	if !foundPreservedSession {
		t.Fatalf("expected SessionStart preserved, got preserved=%v added=%v refreshed=%v", diff.PreservedEvents, diff.AddedEvents, diff.RefreshedEvents)
	}
	if len(diff.AddedEvents) == 0 {
		t.Fatalf("expected at least one Added event for brand-new coverage, got empty")
	}
}

func TestExtractTracearyManagedKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "session start",
			input: "TRACEARY_BIN='traceary' bash '/scripts/traceary-session.sh' 'claude' 'start'",
			want:  "traceary-session.sh:claude:start",
		},
		{
			name:  "compact post variant",
			input: "TRACEARY_BIN='traceary' bash '/scripts/traceary-compact.sh' 'claude' 'post-compact'",
			want:  "traceary-compact.sh:claude:post-compact",
		},
		{
			name:  "compact session-start variant",
			input: "TRACEARY_BIN='traceary' bash '/scripts/traceary-compact.sh' 'claude' 'session-start-compact'",
			want:  "traceary-compact.sh:claude:session-start-compact",
		},
		{
			name:  "audit without action args",
			input: "TRACEARY_BIN='traceary' bash '/scripts/traceary-audit.sh' 'claude'",
			want:  "traceary-audit.sh:claude",
		},
		{
			name:  "direct hook session start",
			input: "'traceary' 'hook' 'session' 'claude' 'start'",
			want:  "traceary-session.sh:claude:start",
		},
		{
			name:  "direct hook compact variant",
			input: "'traceary' 'hook' 'compact' 'claude' 'post-compact'",
			want:  "traceary-compact.sh:claude:post-compact",
		},
		{
			name:  "direct hook prompt",
			input: "'traceary' 'hook' 'prompt' 'claude'",
			want:  "traceary-prompt.sh:claude",
		},
		{
			name:  "direct hook with apostrophe in binary path",
			input: "'/Users/O'\"'\"'Connor/bin/traceary' 'hook' 'session' 'claude' 'start'",
			want:  "traceary-session.sh:claude:start",
		},
		{
			name:  "direct hook with unrelated binary name",
			input: "'custom-cli' 'hook' 'session' 'claude' 'start'",
			want:  "",
		},
		{
			name:  "unrelated command",
			input: "echo hello",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, extractTracearyManagedKey(tt.input)); diff != "" {
				t.Fatalf("extractTracearyManagedKey() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
