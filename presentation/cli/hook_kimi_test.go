package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_HookKimiCoreEvents(t *testing.T) {
	t.Setenv("TRACEARY_HOOK_STATE_DIR", t.TempDir())
	t.Setenv("TRACEARY_HOOK_STATE_KEY", "kimi-core-events")
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")

	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("records session start with Kimi identity", func(t *testing.T) {
		payload := readKimiFixture(t, "session_start.json")
		sessionStub := &sessionUsecaseStub{startEvent: model.EventOf(
			types.EventID("event-kimi-start"),
			types.EventKindSessionStarted,
			types.Client("hook"),
			types.Agent("kimi"),
			types.SessionID("session_00000000-0000-4000-8000-000000000001"),
			types.Workspace("github.com/duck8823/traceary"),
			"session started",
			time.Now(),
		)}

		stdout, _, gotSession := runKimiHook(t, "session-start", payload, nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SessionStart output = %q, want empty passive-hook output", stdout)
		}
		if got, want := gotSession.startCall.sessionID, types.SessionID("session_00000000-0000-4000-8000-000000000001"); got != want {
			t.Fatalf("session ID = %q, want %q", got, want)
		}
		if got, want := gotSession.startCall.client, types.Client("hook"); got != want {
			t.Fatalf("session client = %q, want %q", got, want)
		}
		if got, want := gotSession.startCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("session agent = %q, want %q", got, want)
		}
	})

	t.Run("records session end with Kimi identity", func(t *testing.T) {
		payload := readKimiFixture(t, "session_end.json")
		sessionStub := &sessionUsecaseStub{}

		stdout, _, gotSession := runKimiHook(t, "session-end", payload, nil, sessionStub)

		if stdout != "" {
			t.Fatalf("SessionEnd output = %q, want empty passive-hook output", stdout)
		}
		if got, want := gotSession.endCall.sessionID, types.SessionID("session_00000000-0000-4000-8000-000000000001"); got != want {
			t.Fatalf("session ID = %q, want %q", got, want)
		}
		if got, want := gotSession.endCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("session agent = %q, want %q", got, want)
		}
	})

	t.Run("flattens prompt content blocks and records prompt", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "user-prompt-submit", readKimiFixture(t, "user_prompt_submit.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("UserPromptSubmit output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindPrompt; got != want {
			t.Fatalf("prompt kind = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.message, "Reply with exactly one word: pong."; got != want {
			t.Fatalf("prompt body = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("prompt agent = %q, want %q", got, want)
		}
		if got, want := eventStub.logCall.sourceHook, "user_prompt_submit"; got != want {
			t.Fatalf("prompt source hook = %q, want %q", got, want)
		}
	})

	t.Run("keeps PreToolUse fail open without recording a duplicate audit", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "pre-tool-use", readKimiFixture(t, "pre_tool_use.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PreToolUse output = %q, want empty passive-hook output", stdout)
		}
		if eventStub.auditCall.command != "" {
			t.Fatalf("PreToolUse recorded audit %q, want validation-only boundary", eventStub.auditCall.command)
		}
	})

	t.Run("records completed tool audit from PostToolUse", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-tool-use", readKimiFixture(t, "post_tool_use.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PostToolUse output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.auditCall.command, "echo hello-from-kimi-probe"; got != want {
			t.Fatalf("audit command = %q, want %q", got, want)
		}
		if got, want := eventStub.auditCall.output, "hello-from-kimi-probe\n"; got != want {
			t.Fatalf("audit output = %q, want %q", got, want)
		}
		if got, want := eventStub.auditCall.agent, types.Agent("kimi"); got != want {
			t.Fatalf("audit agent = %q, want %q", got, want)
		}
		if eventStub.auditCall.failed {
			t.Fatal("PostToolUse audit must not be flagged failed")
		}
	})

	t.Run("flags failed tool audit from PostToolUseFailure error object", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-tool-use-failure", readKimiFixture(t, "post_tool_use_failure.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("PostToolUseFailure output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.auditCall.command, "ls /nonexistent-dir"; got != want {
			t.Fatalf("audit command = %q, want %q", got, want)
		}
		if !eventStub.auditCall.failed {
			t.Fatal("PostToolUseFailure audit must be flagged failed")
		}
		if !strings.Contains(eventStub.auditCall.output, "No such file or directory") {
			t.Fatalf("audit output = %q, want the flattened error message", eventStub.auditCall.output)
		}
	})

	t.Run("records transcript from the session wire log on Stop", func(t *testing.T) {
		writeKimiSessionWireLog(t, homeDir, "session_00000000-0000-4000-8000-000000000001", []string{
			`{"type":"metadata","protocol_version":"1.4","created_at":1784466738324}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"think","think":"thinking about the probe"}},"time":1784466739000}`,
			`{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"pong"}},"time":1784466740000}`,
		})

		stdout, eventStub, _ := runKimiHook(t, "stop", readKimiFixture(t, "stop.json"), nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
			t.Fatalf("transcript kind = %q, want %q", got, want)
		}
		if !strings.Contains(eventStub.logCall.message, "pong") {
			t.Fatalf("transcript body = %q, want the wire log text block", eventStub.logCall.message)
		}
		if !strings.Contains(eventStub.logCall.message, "thinking about the probe") {
			t.Fatalf("transcript body = %q, want the wire log thinking block", eventStub.logCall.message)
		}
	})

	t.Run("skips transcript silently when the session index has no entry", func(t *testing.T) {
		payload := strings.Replace(readKimiFixture(t, "stop.json"), "session_00000000-0000-4000-8000-000000000001", "session_99999999-9999-4999-8999-999999999999", 1)

		stdout, eventStub, _ := runKimiHook(t, "stop", payload, nil, nil)

		if stdout != "" {
			t.Fatalf("Stop output = %q, want empty passive-hook output", stdout)
		}
		if eventStub.logCall.kind != "" {
			t.Fatalf("Stop without a session index recorded %q, want silent skip", eventStub.logCall.kind)
		}
	})

	t.Run("keeps malformed payloads fail open", func(t *testing.T) {
		stdout, eventStub, _ := runKimiHook(t, "post-tool-use", "not json", nil, nil)

		if stdout != "" {
			t.Fatalf("malformed PostToolUse output = %q, want empty fail-open output", stdout)
		}
		if eventStub.auditCall.command != "" {
			t.Fatalf("malformed payload recorded audit %q, want fail-open skip", eventStub.auditCall.command)
		}
	})
}

func runKimiHook(
	t *testing.T,
	event string,
	payload string,
	eventStub *eventUsecaseStub,
	sessionStub *sessionUsecaseStub,
	opts ...cli.RootCLIOption,
) (string, *eventUsecaseStub, *sessionUsecaseStub) {
	t.Helper()
	if eventStub == nil {
		eventStub = &eventUsecaseStub{}
	}
	if sessionStub == nil {
		sessionStub = &sessionUsecaseStub{}
	}

	baseOptions := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	}
	rootCmd := newTestRootCLI(append(baseOptions, opts...)...).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "kimi", event})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(hook kimi %s) error = %v", event, err)
	}
	return stdout.String(), eventStub, sessionStub
}

func readKimiFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "kimi_hooks", "v0.27.0", name)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Kimi fixture %s: %v", path, err)
	}
	return string(payload)
}

// writeKimiSessionWireLog seeds a fake Kimi home with a session index entry
// and a wire log for the session, exercising the transcript side channel.
func writeKimiSessionWireLog(t *testing.T, homeDir, sessionID string, wireRows []string) {
	t.Helper()
	kimiHome := filepath.Join(homeDir, ".kimi-code")
	sessionDir := filepath.Join(kimiHome, "sessions", "wd_probe_000000000000", sessionID)
	if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o755); err != nil {
		t.Fatalf("mkdir wire log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), []byte(strings.Join(wireRows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write wire log: %v", err)
	}
	index := `{"sessionId":"` + sessionID + `","sessionDir":"` + sessionDir + `","workDir":"/workspace/kimi-contract-probe"}` + "\n"
	if err := os.WriteFile(filepath.Join(kimiHome, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatalf("write session index: %v", err)
	}
}
