package filesystem

import (
	"strings"
	"testing"
)

func TestKimiHooksHandler_ExposesFailClosedBoundary(t *testing.T) {
	t.Parallel()

	handler := NewKimiHooksHandler()
	if got := handler.Name(); got != "kimi" {
		t.Fatalf("Name() = %q, want kimi", got)
	}
	if got := handler.Build("traceary").Events(); len(got) != 0 {
		t.Fatalf("Build().Events() = %v, want empty (document is rendered by renderDocument)", got)
	}
	if _, err := handler.DefaultInstallPath(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("DefaultInstallPath() error = %v, want fail-closed install error", err)
	}
	if err := handler.validateInstall(); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("validateInstall() error = %v, want fail-closed install error", err)
	}
}

func TestKimiHooksHandler_RendersTOMLHookRules(t *testing.T) {
	t.Parallel()

	handler := NewKimiHooksHandler()
	document, err := handler.renderDocument("traceary")
	if err != nil {
		t.Fatalf("renderDocument() error = %v", err)
	}
	text := string(document)

	// Every rendered rule targets the Kimi TOML [[hooks]] array with the
	// contract-verified event set and the hidden runtime entrypoints.
	expectedEvents := []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit",
		"PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop",
	}
	if got := strings.Count(text, "[[hooks]]"); got != len(expectedEvents) {
		t.Fatalf("rendered [[hooks]] rule count = %d, want %d", got, len(expectedEvents))
	}
	for _, event := range expectedEvents {
		if !strings.Contains(text, `event = "`+event+`"`) {
			t.Errorf("rendered document missing event %q", event)
		}
	}
	for _, action := range []string{
		"session-start", "session-end", "user-prompt-submit",
		"pre-tool-use", "post-tool-use", "post-tool-use-failure", "stop",
	} {
		if !strings.Contains(text, "'traceary' 'hook' 'kimi' '"+action+"'") {
			t.Errorf("rendered document missing runtime command for action %q", action)
		}
	}
	if !strings.Contains(text, "timeout = 5") {
		t.Error("rendered document missing per-hook timeout")
	}
}
