package model_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/model"
)

func TestNewGeminiHooks(t *testing.T) {
	t.Parallel()

	hooks := model.NewGeminiHooks("/scripts", "traceary")

	wantEventOrder := []string{"SessionStart", "SessionEnd", "AfterTool"}
	if diff := cmp.Diff(wantEventOrder, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	t.Run("SessionStart has named command with timeout", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("SessionStart")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(SessionStart entries) = %d, want %d", got, want)
		}
		command := entries[0].Commands()[0]
		if got, want := command.Name(), "traceary-session-start"; got != want {
			t.Fatalf("SessionStart name = %q, want %q", got, want)
		}
		timeout, ok := command.Timeout().Get()
		if !ok || timeout != 5000 {
			t.Fatalf("SessionStart timeout = %d, present=%v, want %d", timeout, ok, 5000)
		}
		if got, want := command.Description(), "Start a Traceary session"; got != want {
			t.Fatalf("SessionStart description = %q, want %q", got, want)
		}
		if got, want := command.ManagedKey(), "traceary-session.sh:gemini:start"; got != want {
			t.Fatalf("SessionStart managed key = %q, want %q", got, want)
		}
	})

	t.Run("AfterTool uses run_shell_command matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("AfterTool")
		if got, want := len(entries), 1; got != want {
			t.Fatalf("len(AfterTool entries) = %d, want %d", got, want)
		}
		matcher, ok := entries[0].Matcher().Get()
		if !ok || matcher != "run_shell_command" {
			t.Fatalf("AfterTool matcher = %q, present=%v, want %q", matcher, ok, "run_shell_command")
		}
		if got, want := entries[0].Commands()[0].Name(), "traceary-audit"; got != want {
			t.Fatalf("AfterTool command name = %q, want %q", got, want)
		}
		if got, want := entries[0].Commands()[0].ManagedKey(), "traceary-audit.sh:gemini"; got != want {
			t.Fatalf("AfterTool managed key = %q, want %q", got, want)
		}
	})
}
