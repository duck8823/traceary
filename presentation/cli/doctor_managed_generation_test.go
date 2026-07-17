package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestManagedHookTimeoutDrift_DetectsStaleTimeout(t *testing.T) {
	extract := filesystem.NewHooksInspector().ExtractManagedKeyFromEntry
	installed := []byte(`{
  "hooks": {
    "BeforeAgent": [{
      "hooks": [{
        "name": "traceary-before-agent",
        "type": "command",
        "command": "'traceary' 'hook' 'prompt' 'gemini'",
        "timeout": 5000
      }]
    }]
  }
}`)
	desired := []byte(`{
  "hooks": {
    "BeforeAgent": [{
      "hooks": [{
        "name": "traceary-before-agent",
        "type": "command",
        "command": "'traceary' 'hook' 'prompt' 'gemini'",
        "timeout": 10000
      }]
    }]
  }
}`)
	reasons := managedHookTimeoutDrift(installed, desired, extract)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "5000ms") || !strings.Contains(reasons[0], "10000ms") {
		t.Fatalf("reasons = %#v", reasons)
	}
	if reasons := managedHookTimeoutDrift(desired, desired, extract); len(reasons) != 0 {
		t.Fatalf("current generation must not drift: %#v", reasons)
	}
}

func TestAttachManagedGenerationCheck_EndToEnd(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(ResetUserHomeDirFunc)

	root := NewRootCLI(
		WithHooksOrchestrator(filesystem.NewHooksOrchestrator(map[string]application.HooksClientHandler{
			"gemini": filesystem.NewGeminiHooksHandler(),
		})),
		WithHooksInspector(filesystem.NewHooksInspector()),
	)
	if root.hooksOrchestrator == nil {
		t.Fatal("hooks orchestrator not wired")
	}
	path, err := root.hooksOrchestrator.Install(context.Background(), "gemini", "traceary", projectDir, types.None[string](), true)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// Downgrade timeouts to the 2026-06 dogfood generation.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.ReplaceAll(string(content), "10000", "5000")
	if stale == string(content) {
		t.Fatal("fixture expected timeout 10000 in generated gemini hooks")
	}
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	check := root.inspectDoctorConfigFile(context.Background(), "gemini", path, projectDir)
	if check.Status != doctorStatusWarn {
		t.Fatalf("check = %#v, want warn for stale generation", check)
	}
	if !strings.Contains(check.Message, "stale") && !strings.Contains(check.Message, "generation") {
		t.Fatalf("message = %q", check.Message)
	}
	if !check.AutoFixAvailable || check.FixFunc == nil {
		t.Fatalf("AutoFix missing: %#v", check)
	}
	if _, err := check.FixFunc(context.Background(), false); err != nil {
		t.Fatalf("fix: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "10000") {
		t.Fatalf("after fix should restore 10000, got:\n%s", after)
	}
	// Re-inspect must pass.
	pass := root.inspectDoctorConfigFile(context.Background(), "gemini", path, projectDir)
	if pass.Status != doctorStatusPass {
		t.Fatalf("after fix status = %#v", pass)
	}

	// Sanity: path lives under the project.
	if !strings.HasPrefix(path, projectDir) && !strings.Contains(path, filepath.Base(projectDir)) {
		t.Logf("install path = %s (projectDir = %s)", path, projectDir)
	}
}
