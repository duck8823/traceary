package filesystem_test

import (
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestGrokHooksHandler_BuildsVerifiedCoreHooks(t *testing.T) {
	t.Parallel()

	handler := filesystem.NewGrokHooksHandler()
	if got := handler.Name(); got != "grok" {
		t.Fatalf("Name() = %q, want grok", got)
	}
	hooks := handler.Build("traceary")
	wantEvents := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "PreCompact", "PostCompact"}
	if got := hooks.EventOrder(); len(got) != len(wantEvents) {
		t.Fatalf("Build().EventOrder() = %v, want %v", got, wantEvents)
	}
	for index, event := range wantEvents {
		if got := hooks.EventOrder()[index]; got != event {
			t.Fatalf("Build().EventOrder()[%d] = %q, want %q", index, got, event)
		}
		entries := hooks.Entries(event)
		if len(entries) != 1 || len(entries[0].Commands()) != 1 {
			t.Fatalf("Build().Entries(%q) = %v, want one command", event, entries)
		}
	}

	projectDir := t.TempDir()
	path, err := handler.DefaultInstallPath(projectDir)
	if err != nil {
		t.Fatalf("DefaultInstallPath() error = %v", err)
	}
	if want := filepath.Join(projectDir, ".grok", "hooks", "traceary.json"); path != want {
		t.Fatalf("DefaultInstallPath() = %q, want %q", path, want)
	}
}
