package main

import (
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
