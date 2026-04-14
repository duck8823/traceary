package filesystem_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestCodexHooksHandler_Build(t *testing.T) {
	t.Parallel()

	handler := filesystem.NewCodexHooksHandler()
	hooks := handler.Build("", "traceary")

	wantEventOrder := []string{"SessionStart", "Stop", "PostToolUse"}
	if diff := cmp.Diff(wantEventOrder, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	t.Run("SessionStart has empty matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("SessionStart")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(SessionStart entries) mismatch (-want +got):\n%s", diff)
		}
		if entries[0].Matcher().IsPresent() {
			value, _ := entries[0].Matcher().Get()
			t.Fatalf("SessionStart matcher should be empty, got %q", value)
		}
		if diff := cmp.Diff("traceary-session.sh:codex:start", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("SessionStart managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'codex' 'start'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("Stop has empty matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("Stop")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(Stop entries) mismatch (-want +got):\n%s", diff)
		}
		if entries[0].Matcher().IsPresent() {
			value, _ := entries[0].Matcher().Get()
			t.Fatalf("Stop matcher should be empty, got %q", value)
		}
		if diff := cmp.Diff("traceary-session.sh:codex:stop", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("Stop managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'codex' 'stop'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("Stop command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("PostToolUse uses empty-string matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostToolUse")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(PostToolUse entries) mismatch (-want +got):\n%s", diff)
		}
		matcher, ok := entries[0].Matcher().Get()
		if diff := cmp.Diff(true, ok); diff != "" {
			t.Fatalf("PostToolUse matcher presence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("", matcher); diff != "" {
			t.Fatalf("PostToolUse matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit.sh:codex", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("PostToolUse managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'audit' 'codex'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("PostToolUse command mismatch (-want +got):\n%s", diff)
		}
	})
}
