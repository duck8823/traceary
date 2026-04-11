package hooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type hooksJSON struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher string     `json:"matcher,omitempty"`
	Hooks   []hookDef  `json:"hooks"`
}

type hookDef struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func TestHooksContract_AllClientsHaveRequiredEvents(t *testing.T) {
	t.Parallel()

	clients := []struct {
		name           string
		hooksPath      string
		requiredEvents []string
	}{
		{
			name:           "claude",
			hooksPath:      "../../integrations/claude-plugin/hooks/hooks.json",
			requiredEvents: []string{"SessionStart", "SessionEnd", "PostToolUse", "PostToolUseFailure", "PostCompact", "UserPromptSubmit"},
		},
		{
			name:           "codex",
			hooksPath:      "../../plugins/traceary/hooks.json",
			requiredEvents: []string{"SessionStart", "Stop", "PostToolUse"},
		},
		{
			name:           "gemini",
			hooksPath:      "../../integrations/gemini-extension/hooks/hooks.json",
			requiredEvents: []string{"SessionStart", "SessionEnd", "AfterTool"},
		},
	}

	for _, client := range clients {
		t.Run(client.name+" has required events", func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(client.hooksPath)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", client.hooksPath, err)
			}

			var hooks hooksJSON
			if err := json.Unmarshal(data, &hooks); err != nil {
				t.Fatalf("Unmarshal error = %v", err)
			}

			for _, event := range client.requiredEvents {
				matchers, ok := hooks.Hooks[event]
				if !ok {
					t.Errorf("missing required event %q", event)
					continue
				}
				if len(matchers) == 0 {
					t.Errorf("event %q has no matchers", event)
					continue
				}
				for _, matcher := range matchers {
					if len(matcher.Hooks) == 0 {
						t.Errorf("event %q matcher has no hooks", event)
					}
					for _, hook := range matcher.Hooks {
						if hook.Type != "command" {
							t.Errorf("event %q hook type = %q, want command", event, hook.Type)
						}
						if hook.Command == "" {
							t.Errorf("event %q hook command is empty", event)
						}
					}
				}
			}
		})
	}
}

func TestHooksContract_AllClientsInvokeTracearyScripts(t *testing.T) {
	t.Parallel()

	clients := []struct {
		name      string
		hooksPath string
	}{
		{name: "claude", hooksPath: "../../integrations/claude-plugin/hooks/hooks.json"},
		{name: "codex", hooksPath: "../../plugins/traceary/hooks.json"},
		{name: "gemini", hooksPath: "../../integrations/gemini-extension/hooks/hooks.json"},
	}

	for _, client := range clients {
		t.Run(client.name+" hooks invoke traceary scripts", func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(client.hooksPath)
			if err != nil {
				t.Fatalf("ReadFile error = %v", err)
			}

			var hooks hooksJSON
			if err := json.Unmarshal(data, &hooks); err != nil {
				t.Fatalf("Unmarshal error = %v", err)
			}

			hasSessionScript := false
			hasAuditScript := false

			for _, matchers := range hooks.Hooks {
				for _, matcher := range matchers {
					for _, hook := range matcher.Hooks {
						if containsSubstring(hook.Command, "traceary-session.sh") {
							hasSessionScript = true
						}
						if containsSubstring(hook.Command, "traceary-audit.sh") {
							hasAuditScript = true
						}
					}
				}
			}

			if !hasSessionScript {
				t.Errorf("%s hooks.json does not invoke traceary-session.sh", client.name)
			}
			if !hasAuditScript {
				t.Errorf("%s hooks.json does not invoke traceary-audit.sh", client.name)
			}
		})
	}
}

func TestHooksContract_ScriptFilesExistForAllClients(t *testing.T) {
	t.Parallel()

	scriptDirs := []struct {
		name string
		dir  string
	}{
		{name: "claude", dir: "../../integrations/claude-plugin/scripts"},
		{name: "codex", dir: "../../plugins/traceary/scripts"},
		{name: "gemini", dir: "../../integrations/gemini-extension/scripts"},
	}

	requiredScripts := []string{"common.sh", "traceary-session.sh", "traceary-audit.sh"}

	for _, client := range scriptDirs {
		t.Run(client.name+" has all required scripts", func(t *testing.T) {
			t.Parallel()

			for _, script := range requiredScripts {
				path := filepath.Join(client.dir, script)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Errorf("missing script %s in %s", script, client.name)
				}
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
