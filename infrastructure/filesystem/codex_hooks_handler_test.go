package filesystem_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestCodexHooksHandler_Build(t *testing.T) {
	t.Parallel()

	handler := filesystem.NewCodexHooksHandler()
	hooks := handler.Build("/scripts", "traceary")

	wantEventOrder := []string{"SessionStart", "Stop", "PostToolUse"}
	if diff := cmp.Diff(wantEventOrder, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	t.Run("SessionStart has empty matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("SessionStart")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(SessionStart entries) = %d, want %d", got, want)
		}
		if entries[0].Matcher().IsPresent() {
			value, _ := entries[0].Matcher().Get()
			t.Fatalf("SessionStart matcher should be empty, got %q", value)
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-session.sh:codex:start"; got != want {
			t.Fatalf("SessionStart managed key = %q, want %q", got, want)
		}
	})

	t.Run("Stop has empty matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("Stop")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(Stop entries) = %d, want %d", got, want)
		}
		if entries[0].Matcher().IsPresent() {
			value, _ := entries[0].Matcher().Get()
			t.Fatalf("Stop matcher should be empty, got %q", value)
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-session.sh:codex:stop"; got != want {
			t.Fatalf("Stop managed key = %q, want %q", got, want)
		}
	})

	t.Run("PostToolUse uses empty-string matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostToolUse")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(PostToolUse entries) = %d, want %d", got, want)
		}
		matcher, ok := entries[0].Matcher().Get()
		if !ok || matcher != "" {
			t.Fatalf("PostToolUse matcher = %q, present=%v, want empty string present", matcher, ok)
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-audit.sh:codex"; got != want {
			t.Fatalf("PostToolUse managed key = %q, want %q", got, want)
		}
	})
}
