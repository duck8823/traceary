package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
	cli "github.com/duck8823/traceary/presentation/cli"
)

// runAntigravityHook executes a hidden `hook antigravity <event>` subcommand
// with the given payload and returns the JSON the runtime wrote to stdout. The
// runtime entrypoints are fail-soft, so Execute() is expected to succeed and
// the test asserts the output contract plus any recorded usecase calls.
func runAntigravityHook(t *testing.T, event, payload string, opts ...cli.RootCLIOption) (string, *eventUsecaseStub, *sessionUsecaseStub) {
	t.Helper()

	eventStub := &eventUsecaseStub{}
	sessionStub := &sessionUsecaseStub{}
	base := []cli.RootCLIOption{
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithEvent(eventStub),
		cli.WithSession(sessionStub),
	}
	rootCmd := newTestRootCLI(append(base, opts...)...).Command()

	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(strings.NewReader(payload))
	rootCmd.SetArgs([]string{"hook", "antigravity", event})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(hook antigravity %s) error = %v", event, err)
	}
	return stdout.String(), eventStub, sessionStub
}

func TestRootCLI_HookAntigravityPreInvocation(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	out, _, sessionStub := runAntigravityHook(t, "pre-invocation",
		`{"conversationId":"conv-1","workspacePaths":["/repo"]}`)

	if out != "{}" {
		t.Fatalf("PreInvocation output = %q, want {}", out)
	}
	if got, want := sessionStub.startCall.sessionID, types.SessionID("conv-1"); got != want {
		t.Fatalf("session start sessionID = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.agent, types.Agent("antigravity"); got != want {
		t.Fatalf("session start agent = %q, want %q", got, want)
	}
	if got, want := sessionStub.startCall.workspace, types.Workspace("github.com/duck8823/traceary"); got != want {
		t.Fatalf("session start workspace = %q, want %q", got, want)
	}
}

func TestRootCLI_HookAntigravityPreInvocationMissingConversationIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	out, _, sessionStub := runAntigravityHook(t, "pre-invocation", `{"workspacePaths":["/repo"]}`)

	if out != "{}" {
		t.Fatalf("PreInvocation output = %q, want {}", out)
	}
	if sessionStub.startCall.sessionID != "" {
		t.Fatalf("session should not start without conversationId, got %q", sessionStub.startCall.sessionID)
	}
}

func TestRootCLI_HookAntigravityToolUsePairing(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// PreToolUse persists the run_command details keyed by conversationId+stepIdx.
	preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-1","stepIdx":"3","toolCall":{"name":"run_command","args":{"CommandLine":"go test ./...","Cwd":"/repo"}}}`)
	if preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want {\"decision\":\"allow\"}", preOut)
	}

	// PostToolUse — carrying only stepIdx/error — pairs the persisted command
	// and records a command audit.
	postOut, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-1","stepIdx":"3","error":""}`)
	if postOut != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", postOut)
	}
	if got, want := eventStub.auditCall.command, "go test ./..."; got != want {
		t.Fatalf("audit command = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.agent, types.Agent("antigravity"); got != want {
		t.Fatalf("audit agent = %q, want %q", got, want)
	}
	if got, want := eventStub.auditCall.sessionID, types.SessionID("conv-1"); got != want {
		t.Fatalf("audit sessionID = %q, want %q", got, want)
	}
	if eventStub.auditCall.failed {
		t.Fatalf("audit should not be flagged failed for an empty error field")
	}
}

func TestRootCLI_HookAntigravityToolUsePairingFlagsError(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	if preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-2","stepIdx":"7","toolCall":{"name":"run_command","args":{"CommandLine":"false","Cwd":"/repo"}}}`); preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	_, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-2","stepIdx":"7","error":"command exited with status 1"}`)
	if !eventStub.auditCall.failed {
		t.Fatalf("audit should be flagged failed when PostToolUse carries an error")
	}
}

func TestRootCLI_HookAntigravityPostToolUseWithoutPendingIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// No PreToolUse ran for this step, so there is no pending command to pair.
	out, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-3","stepIdx":"1","error":""}`)
	if out != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", out)
	}
	if eventStub.auditCall.command != "" {
		t.Fatalf("PostToolUse without pending state must not record an audit, got command %q", eventStub.auditCall.command)
	}
}

func TestRootCLI_HookAntigravityPreToolUseNonRunCommandIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	// A non-run_command tool persists nothing; a following PostToolUse for the
	// same step therefore records no audit.
	preOut, _, _ := runAntigravityHook(t, "pre-tool-use",
		`{"conversationId":"conv-4","stepIdx":"2","toolCall":{"name":"read_file","args":{"Path":"README.md"}}}`)
	if preOut != `{"decision":"allow"}` {
		t.Fatalf("PreToolUse output = %q, want allow", preOut)
	}

	out, eventStub, _ := runAntigravityHook(t, "post-tool-use",
		`{"conversationId":"conv-4","stepIdx":"2","error":""}`)
	if out != "{}" {
		t.Fatalf("PostToolUse output = %q, want {}", out)
	}
	if eventStub.auditCall.command != "" {
		t.Fatalf("non-run_command tool must not record an audit, got command %q", eventStub.auditCall.command)
	}
}

func TestRootCLI_HookAntigravityStopOutputContract(t *testing.T) {
	t.Setenv("TRACEARY_WORKSPACE", "github.com/duck8823/traceary")
	homeDir := t.TempDir()
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)

	out, _, _ := runAntigravityHook(t, "stop",
		`{"conversationId":"conv-1","workspacePaths":["/repo"],"terminationReason":"completed"}`)
	if out != `{"decision":""}` {
		t.Fatalf("Stop output = %q, want {\"decision\":\"\"}", out)
	}
}
