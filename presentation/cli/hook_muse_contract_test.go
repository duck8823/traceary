package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMuseHookWiredActionContract(t *testing.T) {
	t.Parallel()

	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	wired := []struct {
		event  string
		action string
	}{
		{"SessionStart", "session-start"},
		{"UserPromptSubmit", "user-prompt-submit"},
		{"PreToolUse", "pre-tool-use"},
		{"PostToolUse", "post-tool-use"},
		{"PostToolUseFailure", "post-tool-use-failure"},
		{"Stop", "stop"},
		{"PreCompact", "pre-compact"},
		{"PostCompact", "post-compact"},
	}

	hooksPath := filepath.Join(repositoryRoot, "integrations", "muse-plugin", "hooks", "hooks.json")
	hooksBytes, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read Muse hooks.json: %v", err)
	}
	var hooksFile struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(hooksBytes, &hooksFile); err != nil {
		t.Fatalf("decode Muse hooks.json: %v", err)
	}
	if len(hooksFile.Hooks) != len(wired) {
		t.Fatalf("hooks.json events = %d, want %d wired actions", len(hooksFile.Hooks), len(wired))
	}
	for _, item := range wired {
		entries, ok := hooksFile.Hooks[item.event]
		if !ok {
			t.Fatalf("hooks.json missing wired event %s", item.event)
		}
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Fatalf("hooks.json %s must contain exactly one command", item.event)
		}
		want := `"${MUSE_PLUGIN_ROOT}/scripts/traceary-muse.sh" "` + item.action + `"`
		if entries[0].Hooks[0].Command != want {
			t.Fatalf("hooks.json %s command = %q, want %q", item.event, entries[0].Hooks[0].Command, want)
		}
	}

	wrapperPath := filepath.Join(repositoryRoot, "scripts", "hooks", "traceary-muse.sh")
	wrapper, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read Muse wrapper: %v", err)
	}
	for _, item := range wired {
		if !strings.Contains(string(wrapper), item.action) {
			t.Fatalf("wrapper does not allow wired action %s", item.action)
		}
	}

	adapterPath := filepath.Join(repositoryRoot, "presentation", "cli", "hook_muse.go")
	adapter, err := os.ReadFile(adapterPath)
	if err != nil {
		t.Fatalf("read Muse adapter: %v", err)
	}
	for _, item := range wired {
		if !strings.Contains(string(adapter), `"`+item.action+`"`) {
			t.Fatalf("hook_muse.go missing wired action %s", item.action)
		}
	}
}
