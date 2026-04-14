package filesystem_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestGeminiHooksHandler_Build(t *testing.T) {
	t.Parallel()

	handler := filesystem.NewGeminiHooksHandler()
	hooks := handler.Build("traceary")

	wantEventOrder := []string{"SessionStart", "SessionEnd", "AfterTool"}
	if diff := cmp.Diff(wantEventOrder, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	t.Run("SessionStart has named command with timeout", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("SessionStart")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(SessionStart entries) mismatch (-want +got):\n%s", diff)
		}
		command := entries[0].Commands()[0]
		if diff := cmp.Diff("traceary-session-start", command.Name()); diff != "" {
			t.Fatalf("SessionStart name mismatch (-want +got):\n%s", diff)
		}
		timeout, ok := command.Timeout().Get()
		if diff := cmp.Diff(true, ok); diff != "" {
			t.Fatalf("SessionStart timeout presence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(5000, timeout); diff != "" {
			t.Fatalf("SessionStart timeout mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("Start a Traceary session", command.Description()); diff != "" {
			t.Fatalf("SessionStart description mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-session.sh:gemini:start", command.ManagedKey()); diff != "" {
			t.Fatalf("SessionStart managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'gemini' 'start'`, command.Command()); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("AfterTool uses run_shell_command matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("AfterTool")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(AfterTool entries) mismatch (-want +got):\n%s", diff)
		}
		matcher, ok := entries[0].Matcher().Get()
		if diff := cmp.Diff(true, ok); diff != "" {
			t.Fatalf("AfterTool matcher presence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("run_shell_command", matcher); diff != "" {
			t.Fatalf("AfterTool matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("AfterTool command name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit.sh:gemini", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("AfterTool managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'audit' 'gemini'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("AfterTool command mismatch (-want +got):\n%s", diff)
		}
	})
}
