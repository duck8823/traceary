package model_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
)

func TestNewClaudeHooks(t *testing.T) {
	t.Parallel()

	scriptsDir := "/home/user/.config/traceary/hook-scripts"
	tracearyBin := "traceary"

	hooks := model.NewClaudeHooks(scriptsDir, tracearyBin)

	wantEventOrder := []string{
		"SessionStart",
		"SessionEnd",
		"PostToolUse",
		"PostToolUseFailure",
		"PostCompact",
		"UserPromptSubmit",
	}
	if diff := cmp.Diff(wantEventOrder, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	t.Run("SessionStart has wildcard and compact entries", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("SessionStart")
		if got, want := len(entries), 2; got != want {
			t.Fatalf("len(SessionStart entries) = %d, want %d", got, want)
		}

		wildcardMatcher, ok := entries[0].Matcher().Get()
		if !ok || wildcardMatcher != "*" {
			t.Fatalf("SessionStart[0] matcher = %q, present=%v, want %q", wildcardMatcher, ok, "*")
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-session.sh:claude:start"; got != want {
			t.Fatalf("SessionStart[0] managed key = %q, want %q", got, want)
		}
		if !strings.Contains(entries[0].Commands()[0].Command(), "traceary-session.sh") {
			t.Fatalf("SessionStart[0] command does not reference traceary-session.sh: %q", entries[0].Commands()[0].Command())
		}

		compactMatcher, ok := entries[1].Matcher().Get()
		if !ok || compactMatcher != "compact" {
			t.Fatalf("SessionStart[1] matcher = %q, present=%v, want %q", compactMatcher, ok, "compact")
		}
		if got, want := entries[1].Commands()[0].ManagedKey(), "traceary-compact.sh:claude:session-start-compact"; got != want {
			t.Fatalf("SessionStart[1] managed key = %q, want %q", got, want)
		}
	})

	t.Run("PostCompact uses post-compact action", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostCompact")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(PostCompact entries) = %d, want %d", got, want)
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-compact.sh:claude:post-compact"; got != want {
			t.Fatalf("PostCompact managed key = %q, want %q", got, want)
		}
	})

	t.Run("PostToolUse covers Bash and mcp", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostToolUse")
		if got, want := len(entries), 2; got != want {
			t.Fatalf("len(PostToolUse entries) = %d, want %d", got, want)
		}
		firstMatcher, _ := entries[0].Matcher().Get()
		if firstMatcher != "Bash" {
			t.Fatalf("PostToolUse[0] matcher = %q, want %q", firstMatcher, "Bash")
		}
		secondMatcher, _ := entries[1].Matcher().Get()
		if secondMatcher != "mcp__.*" {
			t.Fatalf("PostToolUse[1] matcher = %q, want %q", secondMatcher, "mcp__.*")
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-audit.sh:claude"; got != want {
			t.Fatalf("PostToolUse[0] managed key = %q, want %q", got, want)
		}
	})

	t.Run("UserPromptSubmit references prompt script", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("UserPromptSubmit")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(UserPromptSubmit entries) = %d, want %d", got, want)
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-prompt.sh:claude"; got != want {
			t.Fatalf("UserPromptSubmit managed key = %q, want %q", got, want)
		}
	})
}
