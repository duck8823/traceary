package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

// execAntigravityHook runs a hidden `hook antigravity <event>` subcommand
// against the supplied stubs so a caller can replay a multi-event sequence and
// observe the accumulated effect (the stubs record each call). The runtime
// entrypoints are fail-soft, so Execute() is expected to succeed.
func execAntigravityHook(t *testing.T, event, payload string, eventStub *eventUsecaseStub, sessionStub *sessionUsecaseStub) string {
	t.Helper()

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	).Command()

	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "antigravity", event})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(hook antigravity %s) error = %v", event, err)
	}
	return stdout.String()
}

// TestRootCLI_HookAntigravityWithoutStopCapturesOnlyAvailableSignals verifies
// fail-soft behavior when an older host or interrupted execution omits Stop.
func TestRootCLI_HookAntigravityWithoutStopCapturesOnlyAvailableSignals(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	eventStub := &eventUsecaseStub{}
	sessionStub := &sessionUsecaseStub{}

	// agy --print fires PreInvocation: an idempotent session start.
	if out := execAntigravityHook(t, "pre-invocation",
		`{"conversationId":"print-conv","workspacePaths":["/repo"]}`, eventStub, sessionStub); out != "{}" {
		t.Fatalf("PreInvocation output = %q, want {}", out)
	}
	// A run_command tool call pairs across PreToolUse (carries the command) and
	// PostToolUse (carries the result).
	if out := execAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"print-conv","stepIdx":"1","toolCall":{"name":"run_command","args":{"CommandLine":"traceary --help","Cwd":"/repo"}}}`,
		eventStub, sessionStub); out != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", out)
	}
	if out := execAntigravityHook(t, "post-tool-use",
		`{"conversationId":"print-conv","stepIdx":"1","error":""}`, eventStub, sessionStub); out != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", out)
	}

	// Start IS captured, recorded as client=hook / agent=antigravity — which is
	// why read-side examples must use `traceary list --agent antigravity`, not
	// `--client antigravity`.
	if got, want := sessionStub.startCall.sessionID, types.SessionID("print-conv"); got != want {
		t.Fatalf("session start sessionID = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.client, types.Client("hook"); got != want {
		t.Fatalf("session start client = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.agent, types.Agent("antigravity"); got != want {
		t.Fatalf("session start agent = %q, want %q", got, want)
	}
	// The run_command audit IS captured.
	if got, want := eventStub.auditCall.command, "traceary --help"; got != want {
		t.Fatalf("run_command audit command = %q, want %q", got, want)
	}

	// No Stop fired, so finalization capture does NOT happen: no transcript /
	// log event and no session end (turn boundary).
	if eventStub.logCall.kind != "" {
		t.Fatalf("headless print must not record a transcript/log event, got kind %q", eventStub.logCall.kind)
	}
	if len(sessionStub.endCalls) != 0 {
		t.Fatalf("headless print must not record a session end, got %d end calls", len(sessionStub.endCalls))
	}
}

// TestRootCLI_HookAntigravityStopRecordsTranscriptWhenHostEmitsStop verifies
// current interactive and headless CLI finalization from transcriptPath.
func TestRootCLI_HookAntigravityStopRecordsTranscriptWhenHostEmitsStop(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcriptPath,
		[]byte(strings.Join([]string{
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"current prompt"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","thinking":"brief reasoning","content":"final print answer"}`,
		}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write synthetic transcript: %v", err)
	}

	eventStub := &eventUsecaseStub{}
	sessionStub := &sessionUsecaseStub{}

	payload := fmt.Sprintf(
		`{"conversationId":"stop-conv","workspacePaths":["/repo"],"transcriptPath":%q,"terminationReason":"completed"}`,
		transcriptPath)
	if out := execAntigravityHook(t, "stop", payload, eventStub, sessionStub); out != `{"decision":""}` {
		t.Fatalf("Stop output = %q, want {\"decision\":\"\"}", out)
	}

	if got, want := eventStub.logCall.kind, types.EventKindTranscript; got != want {
		t.Fatalf("Stop transcript event kind = %q, want %q", got, want)
	}
	if !strings.Contains(eventStub.logCall.message, "final print answer") {
		t.Fatalf("Stop transcript body = %q, want it to contain the assistant turn", eventStub.logCall.message)
	}
	if got, want := eventStub.logCall.agent, types.Agent("antigravity"); got != want {
		t.Fatalf("Stop transcript agent = %q, want %q", got, want)
	}
	if len(eventStub.logCalls) != 2 {
		t.Fatalf("Stop log calls = %d, want prompt and transcript", len(eventStub.logCalls))
	}
	if got := eventStub.logCalls[0]; got.kind != types.EventKindPrompt || got.message != "current prompt" || got.sourceHook != "stop_transcript" {
		t.Fatalf("Stop prompt call = %+v, want transcript-derived prompt", got)
	}
}
