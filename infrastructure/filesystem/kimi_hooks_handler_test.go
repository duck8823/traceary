package filesystem

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
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
	// A general PreToolUse hook is deliberately not wired (PostToolUse
	// carries input and output); only the Agent-matched subagent-start
	// rule uses PreToolUse.
	expectedEvents := []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit",
		"PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop",
		"SubagentStop", "PreCompact", "PostCompact",
	}
	if got := strings.Count(text, "[[hooks]]"); got != len(expectedEvents) {
		t.Fatalf("rendered [[hooks]] rule count = %d, want %d", got, len(expectedEvents))
	}
	for _, event := range expectedEvents {
		if !strings.Contains(text, `event = "`+event+`"`) {
			t.Errorf("rendered document missing event %q", event)
		}
	}
	if !strings.Contains(text, `matcher = "Agent"`) {
		t.Error("rendered document missing the Agent matcher on PreToolUse")
	}
	for _, action := range []string{
		"session-start", "session-end", "user-prompt-submit",
		"pre-tool-use", "post-tool-use", "post-tool-use-failure", "stop",
		"subagent-stop", "pre-compact", "post-compact",
	} {
		if !strings.Contains(text, "'traceary' 'hook' 'kimi' '"+action+"'") {
			t.Errorf("rendered document missing runtime command for action %q", action)
		}
	}
	if !strings.Contains(text, "timeout = 5") {
		t.Error("rendered document missing per-hook timeout")
	}
}

func TestKimiHooksHandler_RenderedTOMLParses(t *testing.T) {
	t.Parallel()

	handler := NewKimiHooksHandler()
	document, err := handler.renderDocument("/tmp/traceary bin/traceary")
	if err != nil {
		t.Fatalf("renderDocument() error = %v", err)
	}

	var parsed struct {
		Hooks []struct {
			Event   string `toml:"event"`
			Matcher string `toml:"matcher"`
			Command string `toml:"command"`
			Timeout int    `toml:"timeout"`
		} `toml:"hooks"`
	}
	if err := toml.Unmarshal(document, &parsed); err != nil {
		t.Fatalf("rendered document is not valid TOML: %v\n%s", err, document)
	}
	if len(parsed.Hooks) != 10 {
		t.Fatalf("parsed hook rules = %d, want 10", len(parsed.Hooks))
	}
	agentMatcherRules := 0
	for _, hook := range parsed.Hooks {
		if !strings.Contains(hook.Command, "'/tmp/traceary bin/traceary' 'hook' 'kimi' '") {
			t.Errorf("parsed command %q lost the quoted traceary bin path", hook.Command)
		}
		if hook.Timeout != 5 {
			t.Errorf("parsed timeout = %d, want 5", hook.Timeout)
		}
		if hook.Matcher == "Agent" {
			agentMatcherRules++
		}
	}
	if agentMatcherRules != 1 {
		t.Errorf("parsed rules with Agent matcher = %d, want exactly 1 (subagent-start)", agentMatcherRules)
	}
}
