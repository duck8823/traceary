package filesystem_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestClaudeHooksHandler_Build(t *testing.T) {
	t.Parallel()

	tracearyBin := "traceary"

	handler := filesystem.NewClaudeHooksHandler()
	hooks := handler.Build(tracearyBin)

	wantEventOrder := []string{
		"SessionStart",
		"SessionEnd",
		"Stop",
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
		if diff := cmp.Diff(2, len(entries)); diff != "" {
			t.Fatalf("len(SessionStart entries) mismatch (-want +got):\n%s", diff)
		}

		wildcardMatcher, ok := entries[0].Matcher().Value()
		if diff := cmp.Diff(true, ok); diff != "" {
			t.Fatalf("SessionStart[0] matcher presence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("*", wildcardMatcher); diff != "" {
			t.Fatalf("SessionStart[0] matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-session.sh:claude:start", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("SessionStart[0] managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-session-start", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("SessionStart[0] name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'claude' 'start'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("SessionStart[0] command mismatch (-want +got):\n%s", diff)
		}

		compactMatcher, ok := entries[1].Matcher().Value()
		if diff := cmp.Diff(true, ok); diff != "" {
			t.Fatalf("SessionStart[1] matcher presence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("compact", compactMatcher); diff != "" {
			t.Fatalf("SessionStart[1] matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-compact.sh:claude:session-start-compact", entries[1].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("SessionStart[1] managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-compact-session-start", entries[1].Commands()[0].Name()); diff != "" {
			t.Fatalf("SessionStart[1] name mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("PostCompact uses post-compact action", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostCompact")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(PostCompact entries) mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-compact.sh:claude:post-compact", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("PostCompact managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-compact-post-compact", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("PostCompact name mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("PostToolUse covers Bash, mcp, and built-in tools", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostToolUse")
		if diff := cmp.Diff(3, len(entries)); diff != "" {
			t.Fatalf("len(PostToolUse entries) mismatch (-want +got):\n%s", diff)
		}
		firstMatcher, _ := entries[0].Matcher().Value()
		if diff := cmp.Diff("Bash", firstMatcher); diff != "" {
			t.Fatalf("PostToolUse[0] matcher mismatch (-want +got):\n%s", diff)
		}
		secondMatcher, _ := entries[1].Matcher().Value()
		if diff := cmp.Diff("mcp__.*", secondMatcher); diff != "" {
			t.Fatalf("PostToolUse[1] matcher mismatch (-want +got):\n%s", diff)
		}
		thirdMatcher, _ := entries[2].Matcher().Value()
		wantBuiltin := "Read|Edit|Write|MultiEdit|Grep|Glob|Agent|Task|TodoWrite|WebFetch|WebSearch|NotebookEdit"
		if diff := cmp.Diff(wantBuiltin, thirdMatcher); diff != "" {
			t.Fatalf("PostToolUse[2] matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit.sh:claude", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("PostToolUse[0] managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("PostToolUse[0] name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit.sh:claude", entries[2].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("PostToolUse[2] managed key mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("PostToolUseFailure mirrors PostToolUse matcher set", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostToolUseFailure")
		if diff := cmp.Diff(3, len(entries)); diff != "" {
			t.Fatalf("len(PostToolUseFailure entries) mismatch (-want +got):\n%s", diff)
		}
		firstMatcher, _ := entries[0].Matcher().Value()
		if diff := cmp.Diff("Bash", firstMatcher); diff != "" {
			t.Fatalf("PostToolUseFailure[0] matcher mismatch (-want +got):\n%s", diff)
		}
		secondMatcher, _ := entries[1].Matcher().Value()
		if diff := cmp.Diff("mcp__.*", secondMatcher); diff != "" {
			t.Fatalf("PostToolUseFailure[1] matcher mismatch (-want +got):\n%s", diff)
		}
		thirdMatcher, _ := entries[2].Matcher().Value()
		wantBuiltin := "Read|Edit|Write|MultiEdit|Grep|Glob|Agent|Task|TodoWrite|WebFetch|WebSearch|NotebookEdit"
		if diff := cmp.Diff(wantBuiltin, thirdMatcher); diff != "" {
			t.Fatalf("PostToolUseFailure[2] matcher mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("BuildWithMatcher(minimal) drops the built-in tool row", func(t *testing.T) {
		t.Parallel()

		minimalHooks := handler.BuildWithMatcher(tracearyBin, filesystem.ClaudeMatcherPresetMinimal)
		entries := minimalHooks.Entries("PostToolUse")
		if diff := cmp.Diff(2, len(entries)); diff != "" {
			t.Fatalf("len(PostToolUse entries) mismatch (-want +got):\n%s", diff)
		}
		firstMatcher, _ := entries[0].Matcher().Value()
		if diff := cmp.Diff("Bash", firstMatcher); diff != "" {
			t.Errorf("entries[0] matcher = %q, want Bash", firstMatcher)
		}
		secondMatcher, _ := entries[1].Matcher().Value()
		if diff := cmp.Diff("mcp__.*", secondMatcher); diff != "" {
			t.Errorf("entries[1] matcher = %q, want mcp__.*", secondMatcher)
		}

		failureEntries := minimalHooks.Entries("PostToolUseFailure")
		if diff := cmp.Diff(2, len(failureEntries)); diff != "" {
			t.Fatalf("len(PostToolUseFailure entries) mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("BuildWithMatcher(all) swaps the built-in list for .*", func(t *testing.T) {
		t.Parallel()

		allHooks := handler.BuildWithMatcher(tracearyBin, filesystem.ClaudeMatcherPresetAll)
		entries := allHooks.Entries("PostToolUse")
		if diff := cmp.Diff(3, len(entries)); diff != "" {
			t.Fatalf("len(PostToolUse entries) mismatch (-want +got):\n%s", diff)
		}
		thirdMatcher, _ := entries[2].Matcher().Value()
		if diff := cmp.Diff(".*", thirdMatcher); diff != "" {
			t.Errorf("entries[2] matcher = %q, want .*", thirdMatcher)
		}
	})

	t.Run("BuildWithMatcher(default) matches plain Build output", func(t *testing.T) {
		t.Parallel()

		defaultHooks := handler.BuildWithMatcher(tracearyBin, filesystem.ClaudeMatcherPresetDefault)
		baselineHooks := handler.Build(tracearyBin)

		got := defaultHooks.Entries("PostToolUse")
		want := baselineHooks.Entries("PostToolUse")
		if diff := cmp.Diff(len(want), len(got)); diff != "" {
			t.Fatalf("default preset vs Build() PostToolUse length mismatch (-want +got):\n%s", diff)
		}
		for i := range want {
			wantMatcher, _ := want[i].Matcher().Value()
			gotMatcher, _ := got[i].Matcher().Value()
			if diff := cmp.Diff(wantMatcher, gotMatcher); diff != "" {
				t.Errorf("default preset entries[%d] matcher mismatch (-want +got):\n%s", i, diff)
			}
		}
	})

	t.Run("UserPromptSubmit references prompt script", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("UserPromptSubmit")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(UserPromptSubmit entries) mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-prompt.sh:claude", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("UserPromptSubmit managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-prompt", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("UserPromptSubmit name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(true, strings.Contains(entries[0].Commands()[0].Command(), "'hook' 'prompt' 'claude'")); diff != "" {
			t.Fatalf("UserPromptSubmit command mismatch (-want +got):\n%s", diff)
		}
	})
}
