package cli

import (
	"context"
	"encoding/json"
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

// TestGeminiConfig_StaleGeneration_ReportsAutoFix verifies that when installed
// hook timeouts lag the current generation (5000 vs 10000) the check is WARN,
// message names both the installed and desired values, and auto-fix is wired.
func TestGeminiConfig_StaleGeneration_ReportsAutoFix(t *testing.T) {
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

	path, err := root.hooksOrchestrator.Install(context.Background(), "gemini", "traceary", projectDir, types.None[string](), true)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

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
		t.Fatalf("status = %s, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "5000ms") || !strings.Contains(check.Message, "10000ms") {
		t.Errorf("message should contain both installed=5000ms and desired=10000ms, got: %q", check.Message)
	}
	if !strings.Contains(check.Message, "traceary doctor --fix --client gemini") {
		t.Errorf("message should name the fix command, got: %q", check.Message)
	}
	if !strings.Contains(check.Hint, "auto-fix") {
		t.Errorf("hint should mention auto-fix, got: %q", check.Hint)
	}
	if !strings.Contains(check.FixCommand, "--dry-run") {
		t.Errorf("FixCommand should be the dry-run preview, got: %q", check.FixCommand)
	}
	if !check.AutoFixAvailable {
		t.Error("AutoFixAvailable must be true for stale generation")
	}
	if check.FixFunc == nil {
		t.Error("FixFunc must be non-nil for stale generation")
	}
}

// TestGeminiConfig_FixApply_UpdatesOnlyTracearyEntries seeds settings.json
// with stale Traceary timeouts plus a non-Traceary top-level key, then:
//   - verifies dry-run leaves the file unchanged
//   - verifies apply restores 10000ms timeouts
//   - verifies the non-Traceary key is preserved byte-for-byte
//   - verifies a subsequent doctor inspect returns pass
func TestGeminiConfig_FixApply_UpdatesOnlyTracearyEntries(t *testing.T) {
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

	path, err := root.hooksOrchestrator.Install(context.Background(), "gemini", "traceary", projectDir, types.None[string](), true)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// Inject a non-Traceary top-level key alongside the generated hooks.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["custom_user_setting"] = json.RawMessage(`"must-survive"`)
	mixed, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Stale the Traceary-managed timeouts.
	stale := strings.ReplaceAll(string(mixed), "10000", "5000")
	if stale == string(mixed) {
		t.Fatal("fixture expected timeout 10000 in generated gemini hooks")
	}
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	check := root.inspectDoctorConfigFile(context.Background(), "gemini", path, projectDir)
	if check.Status != doctorStatusWarn {
		t.Fatalf("status = %s, want warn before fix", check.Status)
	}

	// Dry-run must not modify the file.
	beforeFix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := check.FixFunc(context.Background(), true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	afterDry, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeFix) != string(afterDry) {
		t.Error("dry-run must not modify the file")
	}

	// Apply must restore 10000ms timeouts.
	if _, err := check.FixFunc(context.Background(), false); err != nil {
		t.Fatalf("fix apply: %v", err)
	}
	afterFix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(afterFix), "10000") {
		t.Errorf("after fix should contain 10000ms timeout, got:\n%s", afterFix)
	}

	// Non-Traceary key must survive.
	var afterDoc map[string]json.RawMessage
	if err := json.Unmarshal(afterFix, &afterDoc); err != nil {
		t.Fatalf("unmarshal after fix: %v", err)
	}
	if string(afterDoc["custom_user_setting"]) != `"must-survive"` {
		t.Errorf("non-Traceary key was not preserved; got %s", afterDoc["custom_user_setting"])
	}

	// Re-inspect must pass.
	pass := root.inspectDoctorConfigFile(context.Background(), "gemini", path, projectDir)
	if pass.Status != doctorStatusPass {
		t.Fatalf("after fix status = %s, want pass", pass.Status)
	}
}

// TestGeminiConfig_DocPresence asserts that both language variants of the
// post-upgrade-plugins docs include the required managed hook generation
// refresh step for the Gemini client.
func TestGeminiConfig_DocPresence(t *testing.T) {
	files := []string{
		"../../docs/release/post-upgrade-plugins.md",
		"../../docs/release/post-upgrade-plugins.ja.md",
	}
	required := []string{
		"traceary doctor --fix",
		"--client gemini",
	}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, want := range required {
			if !strings.Contains(string(content), want) {
				t.Errorf("%s: missing required string %q", f, want)
			}
		}
	}
}
