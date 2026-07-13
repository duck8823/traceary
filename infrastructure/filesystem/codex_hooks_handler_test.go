package filesystem_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestCodexHooksHandler_Build(t *testing.T) {
	t.Parallel()

	handler := filesystem.NewCodexHooksHandler()
	hooks := handler.Build("traceary")

	wantEventOrder := []string{"SessionStart", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "UserPromptSubmit", "Stop", "PostToolUse"}
	if diff := cmp.Diff(wantEventOrder, hooks.EventOrder()); diff != "" {
		t.Fatalf("EventOrder() mismatch (-want +got):\n%s", diff)
	}

	for _, tc := range []struct {
		event, name, key, command string
	}{
		{"SubagentStart", "traceary-subagent-start", "traceary-subagent-start.sh:codex", `'traceary' 'hook' 'subagent-start' 'codex'`},
		{"SubagentStop", "traceary-subagent-stop", "traceary-subagent-stop.sh:codex", `'traceary' 'hook' 'subagent-stop' 'codex'`},
		{"PreCompact", "traceary-compact-pre-compact", "traceary-compact.sh:codex:pre-compact", `'traceary' 'hook' 'compact' 'codex' 'pre-compact'`},
		{"PostCompact", "traceary-compact-post-compact", "traceary-compact.sh:codex:post-compact", `'traceary' 'hook' 'compact' 'codex' 'post-compact'`},
	} {
		tc := tc
		t.Run(tc.event+" references its runtime", func(t *testing.T) {
			t.Parallel()
			entries := hooks.Entries(tc.event)
			if diff := cmp.Diff(1, len(entries)); diff != "" {
				t.Fatalf("len(%s entries) mismatch (-want +got):\n%s", tc.event, diff)
			}
			if _, ok := entries[0].Matcher().Value(); ok {
				t.Fatalf("%s matcher should be empty", tc.event)
			}
			command := entries[0].Commands()[0]
			if diff := cmp.Diff(tc.name, command.Name()); diff != "" {
				t.Fatalf("%s name mismatch (-want +got):\n%s", tc.event, diff)
			}
			if diff := cmp.Diff(tc.key, command.ManagedKey()); diff != "" {
				t.Fatalf("%s managed key mismatch (-want +got):\n%s", tc.event, diff)
			}
			if diff := cmp.Diff(tc.command, command.Command()); diff != "" {
				t.Fatalf("%s command mismatch (-want +got):\n%s", tc.event, diff)
			}
		})
	}

	t.Run("UserPromptSubmit references prompt runtime", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("UserPromptSubmit")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(UserPromptSubmit entries) mismatch (-want +got):\n%s", diff)
		}
		if _, ok := entries[0].Matcher().Value(); ok {
			value, _ := entries[0].Matcher().Value()
			t.Fatalf("UserPromptSubmit matcher should be empty, got %q", value)
		}
		if diff := cmp.Diff("traceary-prompt.sh:codex", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("UserPromptSubmit managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-prompt", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("UserPromptSubmit name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'prompt' 'codex'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("UserPromptSubmit command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("SessionStart has empty matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("SessionStart")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(SessionStart entries) mismatch (-want +got):\n%s", diff)
		}
		if _, ok := entries[0].Matcher().Value(); ok {
			value, _ := entries[0].Matcher().Value()
			t.Fatalf("SessionStart matcher should be empty, got %q", value)
		}
		if diff := cmp.Diff("traceary-session.sh:codex:start", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("SessionStart managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-session-start", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("SessionStart name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'codex' 'start'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("SessionStart command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("Stop fires transcript then session-stop in one entry", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("Stop")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(Stop entries) mismatch (-want +got):\n%s", diff)
		}
		if _, ok := entries[0].Matcher().Value(); ok {
			value, _ := entries[0].Matcher().Value()
			t.Fatalf("Stop matcher should be empty, got %q", value)
		}
		commands := entries[0].Commands()
		if diff := cmp.Diff(2, len(commands)); diff != "" {
			t.Fatalf("len(Stop commands) mismatch (-want +got):\n%s", diff)
		}
		// Transcript MUST run before session-stop: session-stop
		// clears the cached session / workspace state files as part
		// of its teardown. If the Codex payload ever omits
		// `session_id` (payload drift, locally patched build),
		// running session-stop first would leave transcript with an
		// empty state-file fallback. The reverse order also records
		// the transcript event before the session is marked ended,
		// which is chronologically accurate.
		if diff := cmp.Diff("traceary-transcript.sh:codex", commands[0].ManagedKey()); diff != "" {
			t.Fatalf("Stop[0] managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-transcript", commands[0].Name()); diff != "" {
			t.Fatalf("Stop[0] name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'transcript' 'codex'`, commands[0].Command()); diff != "" {
			t.Fatalf("Stop[0] command mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-session.sh:codex:stop", commands[1].ManagedKey()); diff != "" {
			t.Fatalf("Stop[1] managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-session-stop", commands[1].Name()); diff != "" {
			t.Fatalf("Stop[1] name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'session' 'codex' 'stop'`, commands[1].Command()); diff != "" {
			t.Fatalf("Stop[1] command mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("PostToolUse uses empty-string matcher", func(t *testing.T) {
		t.Parallel()

		entries := hooks.Entries("PostToolUse")
		if diff := cmp.Diff(1, len(entries)); diff != "" {
			t.Fatalf("len(PostToolUse entries) mismatch (-want +got):\n%s", diff)
		}
		matcher, ok := entries[0].Matcher().Value()
		if diff := cmp.Diff(true, ok); diff != "" {
			t.Fatalf("PostToolUse matcher presence mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("", matcher); diff != "" {
			t.Fatalf("PostToolUse matcher mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit.sh:codex", entries[0].Commands()[0].ManagedKey()); diff != "" {
			t.Fatalf("PostToolUse managed key mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("traceary-audit", entries[0].Commands()[0].Name()); diff != "" {
			t.Fatalf("PostToolUse name mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(`'traceary' 'hook' 'audit' 'codex'`, entries[0].Commands()[0].Command()); diff != "" {
			t.Fatalf("PostToolUse command mismatch (-want +got):\n%s", diff)
		}
	})
}
