package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyIntegrations_PassesOnCurrentTree is the Go equivalent of running
// scripts/verify_integrations.py against the repository: the current tree must
// be consistent. The CLI smoke (Codex removed-command stubs) is skipped here so
// the test stays fast; CI runs `go run ./cmd/repo-tooling integrations verify`
// with the smoke enabled.
func TestVerifyIntegrations_PassesOnCurrentTree(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	if err := verifyIntegrations(root, false); err != nil {
		t.Fatalf("verifyIntegrations() error = %v", err)
	}
}

// TestVerifyIntegrations_FailsWhenRootIncomplete pins that the verifier fails
// loudly on a tree missing the expected files rather than passing vacuously.
func TestVerifyIntegrations_FailsWhenRootIncomplete(t *testing.T) {
	t.Parallel()

	if err := verifyIntegrations(t.TempDir(), false); err == nil {
		t.Fatal("verifyIntegrations() on an empty tree = nil, want an error")
	}
}

func TestCheckNoDuplicateTracearyHookEntries_FailsOnDuplicateManagedEntry(t *testing.T) {
	t.Parallel()

	err := checkNoDuplicateTracearyHookEntries("plugins/traceary/hooks.json", hookFile{
		Hooks: map[string][]hookEntry{
			"PostToolUse": {
				{
					Matcher: "",
					Hooks: []hookCommand{
						{Name: "traceary-audit", Type: "command", Command: "'traceary' 'hook' 'audit' 'codex'"},
						{Name: "traceary-audit", Type: "command", Command: "'traceary' 'hook' 'audit' 'codex'"},
						{Name: "user-audit", Type: "command", Command: "echo user"},
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("checkNoDuplicateTracearyHookEntries() error = nil, want duplicate error")
	}
	if !strings.Contains(err.Error(), "PostToolUse") || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want duplicate PostToolUse message", err)
	}
}

func TestCheckNoDuplicateTracearyHookEntries_AllowsDistinctMatchers(t *testing.T) {
	t.Parallel()

	err := checkNoDuplicateTracearyHookEntries("integrations/claude-plugin/hooks/hooks.json", hookFile{
		Hooks: map[string][]hookEntry{
			"PostToolUse": {
				{
					Matcher: "Bash",
					Hooks:   []hookCommand{{Type: "command", Command: "'traceary' 'hook' 'audit' 'claude'"}},
				},
				{
					Matcher: "mcp__.*",
					Hooks:   []hookCommand{{Type: "command", Command: "'traceary' 'hook' 'audit' 'claude'"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("checkNoDuplicateTracearyHookEntries() error = %v, want nil for distinct matchers", err)
	}
}

func TestRequirePackagedHookCommand_FailsOnMatcherDrift(t *testing.T) {
	t.Parallel()

	err := requirePackagedHookCommand("integrations/gemini-extension/hooks/hooks.json", hookFile{
		Hooks: map[string][]hookEntry{
			"AfterTool": {
				{
					Matcher: "*",
					Hooks: []hookCommand{
						{Name: "traceary-audit", Type: "command", Command: "'traceary' 'hook' 'audit' 'gemini'"},
					},
				},
			},
		},
	}, "AfterTool", "run_shell_command", "traceary-audit", "'hook' 'audit' 'gemini'")
	if err == nil {
		t.Fatal("requirePackagedHookCommand() error = nil, want matcher drift error")
	}
	if !strings.Contains(err.Error(), "AfterTool") || !strings.Contains(err.Error(), "run_shell_command") {
		t.Fatalf("error = %v, want AfterTool/run_shell_command drift message", err)
	}
}

func TestRequirePackagedHookCommand_PassesOnExpectedCommand(t *testing.T) {
	t.Parallel()

	err := requirePackagedHookCommand("integrations/gemini-extension/hooks/hooks.json", hookFile{
		Hooks: map[string][]hookEntry{
			"AfterTool": {
				{
					Matcher: "run_shell_command",
					Hooks: []hookCommand{
						{Name: "traceary-audit", Type: "command", Command: "'traceary' 'hook' 'audit' 'gemini'"},
					},
				},
			},
		},
	}, "AfterTool", "run_shell_command", "traceary-audit", "'hook' 'audit' 'gemini'")
	if err != nil {
		t.Fatalf("requirePackagedHookCommand() error = %v, want nil", err)
	}
}

func TestCheckGrokHooksRejectsContractDrift(t *testing.T) {
	t.Parallel()
	valid := grokHookFixture()
	if err := checkGrokHooks("hooks.json", valid); err != nil {
		t.Fatalf("checkGrokHooks(valid) error = %v", err)
	}

	valid.Hooks["Stop"][0].Hooks[0].Command = `"${GROK_PLUGIN_ROOT}/scripts/traceary-grok.sh" "pre-compact"`
	if err := checkGrokHooks("hooks.json", valid); err == nil {
		t.Fatal("checkGrokHooks(action swap) error = nil")
	}
	valid = grokHookFixture()
	valid.Hooks["Stop"][0].Hooks[0].Timeout = 4
	if err := checkGrokHooks("hooks.json", valid); err == nil {
		t.Fatal("checkGrokHooks(timeout drift) error = nil")
	}
	valid = grokHookFixture()
	valid.Hooks["SubagentStart"] = valid.Hooks["SessionStart"]
	if err := checkGrokHooks("hooks.json", valid); err == nil {
		t.Fatal("checkGrokHooks(extra event) error = nil")
	}
}

func grokHookFixture() hookFile {
	hooks := map[string][]hookEntry{}
	for _, spec := range []struct{ event, name, action string }{
		{"SessionStart", "traceary-session-start", "session-start"},
		{"UserPromptSubmit", "traceary-prompt", "user-prompt-submit"},
		{"PreToolUse", "traceary-tool-pre", "pre-tool-use"},
		{"PostToolUse", "traceary-audit", "post-tool-use"},
		{"Stop", "traceary-stop", "stop"},
		{"PreCompact", "traceary-compact-pre", "pre-compact"},
		{"PostCompact", "traceary-compact-post", "post-compact"},
	} {
		hooks[spec.event] = []hookEntry{{Hooks: []hookCommand{{
			Name: spec.name, Type: "command", Timeout: 5,
			Command: `"${GROK_PLUGIN_ROOT}/scripts/traceary-grok.sh" "` + spec.action + `"`,
		}}}}
	}
	return hookFile{Hooks: hooks}
}

func TestIntegrationHookCopies_MembershipMatrix(t *testing.T) {
	t.Parallel()

	// Pin the single-source matrix so a host package cannot silently drop out of
	// drift checking (or gain an unexpected shared script requirement).
	wantByBase := map[string][]string{
		"common.sh": {
			"integrations/claude-plugin/scripts",
			"plugins/traceary/scripts",
			"integrations/gemini-extension/scripts",
		},
		"traceary-session.sh": {
			"integrations/claude-plugin/scripts",
			"plugins/traceary/scripts",
			"integrations/gemini-extension/scripts",
		},
		"traceary-audit.sh": {
			"integrations/claude-plugin/scripts",
			"plugins/traceary/scripts",
			"integrations/gemini-extension/scripts",
		},
		"traceary-prompt.sh": {
			"integrations/claude-plugin/scripts",
			"integrations/gemini-extension/scripts",
		},
		"traceary-compact.sh": {
			"integrations/claude-plugin/scripts",
		},
		"traceary-grok.sh": {
			"integrations/grok-plugin/scripts",
		},
	}

	gotByBase := make(map[string][]string, len(integrationHookCopies))
	for _, copy := range integrationHookCopies {
		base := filepath.Base(copy.source)
		if !strings.HasPrefix(copy.source, "scripts/hooks/") {
			t.Fatalf("source %q must live under scripts/hooks/", copy.source)
		}
		gotByBase[base] = append([]string(nil), copy.packages...)
	}

	if len(gotByBase) != len(wantByBase) {
		t.Fatalf("integrationHookCopies covers %d scripts, want %d", len(gotByBase), len(wantByBase))
	}
	for base, wantPkgs := range wantByBase {
		gotPkgs, ok := gotByBase[base]
		if !ok {
			t.Fatalf("missing membership for %s", base)
		}
		if len(gotPkgs) != len(wantPkgs) {
			t.Fatalf("%s packages = %v, want %v", base, gotPkgs, wantPkgs)
		}
		for i := range wantPkgs {
			if gotPkgs[i] != wantPkgs[i] {
				t.Fatalf("%s packages = %v, want %v", base, gotPkgs, wantPkgs)
			}
		}
	}
}

func TestCheckHooksAreCopied_DetectsDriftAndMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeHookTree(t, root)

	if err := checkHooksAreCopied(root); err != nil {
		t.Fatalf("checkHooksAreCopied(synced tree) error = %v", err)
	}

	// Drift a shared copy.
	driftPath := filepath.Join(root, "integrations/claude-plugin/scripts/common.sh")
	if err := os.WriteFile(driftPath, []byte("#!/bin/bash\necho drifted\n"), 0o755); err != nil {
		t.Fatalf("WriteFile drift: %v", err)
	}
	err := checkHooksAreCopied(root)
	if err == nil {
		t.Fatal("checkHooksAreCopied(drifted) error = nil, want drift error")
	}
	if !strings.Contains(err.Error(), "drifted") {
		t.Fatalf("error = %v, want drifted message", err)
	}

	// Restore, then remove a package-specific copy.
	if err := os.WriteFile(driftPath, []byte("#!/bin/bash\n# common\n"), 0o755); err != nil {
		t.Fatalf("restore common.sh: %v", err)
	}
	missing := filepath.Join(root, "integrations/grok-plugin/scripts/traceary-grok.sh")
	if err := os.Remove(missing); err != nil {
		t.Fatalf("Remove grok script: %v", err)
	}
	err = checkHooksAreCopied(root)
	if err == nil {
		t.Fatal("checkHooksAreCopied(missing) error = nil, want missing error")
	}
	if !strings.Contains(err.Error(), "missing packaged hook script") {
		t.Fatalf("error = %v, want missing packaged hook script", err)
	}
}

func TestSyncHookCopies_RewritesPackagedScripts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeHookTree(t, root)

	// Corrupt a destination, then sync from canonical.
	target := filepath.Join(root, "plugins/traceary/scripts/traceary-audit.sh")
	if err := os.WriteFile(target, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("WriteFile stale audit: %v", err)
	}

	n, err := syncHookCopies(root)
	if err != nil {
		t.Fatalf("syncHookCopies() error = %v", err)
	}
	wantN := 0
	for _, copy := range integrationHookCopies {
		wantN += len(copy.packages)
	}
	if n != wantN {
		t.Fatalf("syncHookCopies() wrote %d files, want %d", n, wantN)
	}
	if err := checkHooksAreCopied(root); err != nil {
		t.Fatalf("checkHooksAreCopied after sync error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile after sync: %v", err)
	}
	if string(got) != "#!/bin/bash\n# audit\n" {
		t.Fatalf("audit copy = %q, want canonical content", got)
	}
}

// writeHookTree creates a minimal scripts/hooks + package-copy tree that
// satisfies checkHooksAreCopied for the membership matrix.
func writeHookTree(t *testing.T, root string) {
	t.Helper()

	canonical := map[string]string{
		"common.sh":           "#!/bin/bash\n# common\n",
		"traceary-session.sh": "#!/bin/bash\n# session\n",
		"traceary-audit.sh":   "#!/bin/bash\n# audit\n",
		"traceary-prompt.sh":  "#!/bin/bash\n# prompt\n",
		"traceary-compact.sh": "#!/bin/bash\n# compact\n",
		"traceary-grok.sh":    "#!/bin/sh\n# grok\n",
	}
	hooksDir := filepath.Join(root, "scripts/hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll hooks: %v", err)
	}
	for name, body := range canonical {
		path := filepath.Join(hooksDir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	n, err := syncHookCopies(root)
	if err != nil {
		t.Fatalf("seed syncHookCopies: %v", err)
	}
	if n == 0 {
		t.Fatal("seed syncHookCopies wrote 0 files")
	}
}
