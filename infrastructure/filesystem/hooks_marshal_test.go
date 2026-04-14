package filesystem

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestMarshalHooks_RendersCanonicalDocument(t *testing.T) {
	t.Parallel()

	command := model.HookCommandOf(
		"hook-name",
		"command",
		"'traceary' 'hook' 'session' 'claude' 'start'",
		types.Some(5000),
		"Start a Traceary session",
		"traceary-session.sh:claude:start",
	)
	hooks := model.HooksOf(
		[]string{"SessionStart"},
		map[string][]model.HookEntry{
			"SessionStart": {
				model.HookEntryOf(types.Some("*"), []model.HookCommand{command}),
			},
		},
	)

	encoded, err := marshalHooks(hooks)
	if err != nil {
		t.Fatalf("marshalHooks() error = %v", err)
	}

	var decoded hookSettingsDocument
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	sessionStart, ok := decoded.Hooks["SessionStart"]
	if !ok {
		t.Fatalf("SessionStart not found in decoded output")
	}
	if got, want := len(sessionStart), 1; got != want {
		t.Fatalf("len(SessionStart) = %d, want %d", got, want)
	}
	if sessionStart[0].Matcher == nil || *sessionStart[0].Matcher != "*" {
		t.Fatalf("SessionStart matcher = %v, want %q", sessionStart[0].Matcher, "*")
	}
	gotCommand := sessionStart[0].Hooks[0]
	wantCommand := hookCommandDocument{
		Name:        "hook-name",
		Type:        "command",
		Command:     "'traceary' 'hook' 'session' 'claude' 'start'",
		Timeout:     intPointer(5000),
		Description: "Start a Traceary session",
	}
	if diff := cmp.Diff(wantCommand, gotCommand); diff != "" {
		t.Fatalf("decoded command mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalHooks_OmitsOptionalFields(t *testing.T) {
	t.Parallel()

	command := model.HookCommandOf("", "command", "echo hi", types.None[int](), "", "")
	hooks := model.HooksOf(
		[]string{"SessionStart"},
		map[string][]model.HookEntry{
			"SessionStart": {
				model.HookEntryOf(types.None[string](), []model.HookCommand{command}),
			},
		},
	)

	encoded, err := marshalHooks(hooks)
	if err != nil {
		t.Fatalf("marshalHooks() error = %v", err)
	}

	payload := string(encoded)
	if contains(payload, "name") {
		t.Fatalf("marshal output should omit name field when empty: %s", payload)
	}
	if contains(payload, "timeout") {
		t.Fatalf("marshal output should omit timeout field when empty: %s", payload)
	}
	if contains(payload, "description") {
		t.Fatalf("marshal output should omit description field when empty: %s", payload)
	}
	if contains(payload, "matcher") {
		t.Fatalf("marshal output should omit matcher field when empty: %s", payload)
	}
}

func intPointer(value int) *int { return &value }

func contains(value string, substr string) bool {
	for index := 0; index+len(substr) <= len(value); index++ {
		if value[index:index+len(substr)] == substr {
			return true
		}
	}
	return false
}
